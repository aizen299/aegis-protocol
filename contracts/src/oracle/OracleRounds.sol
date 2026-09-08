// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {
    AccessControlUpgradeable
} from "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";
import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import {PausableUpgradeable} from "@openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol";
import {
    EIP712Upgradeable
} from "@openzeppelin/contracts-upgradeable/utils/cryptography/EIP712Upgradeable.sol";
import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import {Math} from "@openzeppelin/contracts/utils/math/Math.sol";

import {Roles} from "../shared/access/Roles.sol";
import {IOracleReader, IOracleRounds} from "./interfaces/IOracleRounds.sol";
import {IOracleStaking} from "./interfaces/IOracleStaking.sol";

/// @title OracleRounds (v0.2)
/// @notice Round lifecycle, signed submissions, and on-chain medianization.
/// @dev Deliberately separate from OracleStaking. Stake accounting custodies balances and should be
///      upgraded rarely; round logic is where iteration happens. Splitting them also keeps both
///      clear of the 24KB limit with room for v1.0 hardening. See docs/v0.2-oracle-plan.md.
///
///      Storage layout is append-only. Run `make contracts-layout-check` before any upgrade.
contract OracleRounds is
    Initializable,
    UUPSUpgradeable,
    AccessControlUpgradeable,
    PausableUpgradeable,
    EIP712Upgradeable,
    IOracleRounds,
    IOracleReader
{
    uint256 private constant _BPS_DENOMINATOR = 10_000;

    /// @dev Bounds the gas of the on-chain median sort. The node set is protocol-controlled, so
    ///      this is a bounded loop over trusted data rather than over user-supplied input.
    uint256 private constant _MAX_SUBMISSIONS = 31;

    bytes32 private constant _SUBMISSION_TYPEHASH =
        keccak256("Submission(uint256 roundId,bytes32 feedId,uint256 value,address node,uint256 nonce)");

    struct Feed {
        bool registered;
        // The scale a feed's values are reported in. The contract cannot enforce it — it medians
        // whatever numbers nodes submit — but declaring it here gives nodes and the indexer one
        // place to read it from, rather than each assuming 18 independently.
        uint8 decimals;
        uint256 currentRoundId;
        uint256 lastSettledRoundId;
    }

    // --- storage (append-only) ---
    IOracleStaking private _staking;
    uint256 private _roundDuration;
    uint256 private _quorumBps;
    uint256 private _minQuorumNodes;
    uint256 private _nextRoundId;
    mapping(bytes32 feedId => Feed feed) private _feeds;
    mapping(uint256 roundId => Round round) private _rounds;
    mapping(uint256 roundId => uint256[] values) private _roundValues;
    mapping(uint256 roundId => mapping(address node => bool submitted)) private _submitted;
    mapping(address node => uint256 nonce) private _nonces;

    // slither-disable-next-line unused-state
    uint256[41] private __gap;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(
        address admin,
        address staking_,
        uint256 roundDuration_,
        uint256 quorumBps_,
        uint256 minQuorumNodes_
    ) external initializer {
        if (admin == address(0) || staking_ == address(0)) revert ZeroAddress();
        if (quorumBps_ == 0 || quorumBps_ > _BPS_DENOMINATOR) revert InvalidBps(quorumBps_);
        if (roundDuration_ == 0 || minQuorumNodes_ == 0) revert ZeroValue();

        __UUPSUpgradeable_init();
        __AccessControl_init();
        __Pausable_init();
        __EIP712_init("AegisOracle", "1");

        _staking = IOracleStaking(staking_);
        _roundDuration = roundDuration_;
        _quorumBps = quorumBps_;
        _minQuorumNodes = minQuorumNodes_;
        _nextRoundId = 1;

        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    // --- rounds ---

    /// @inheritdoc IOracleRounds
    /// @dev Permissionless: requiring a keyed keeper would make liveness depend on one key.
    ///
    ///      The eligible set and its size are frozen here. Evaluating quorum against the live set
    ///      at settlement would let nodes shrink the denominator by deactivating mid-round, so a
    ///      round that should have failed settles on fewer submissions than policy intends.
    function openRound(
        bytes32 feedId
    ) external whenNotPaused returns (uint256 roundId) {
        Feed storage feed = _feeds[feedId];
        if (!feed.registered) revert FeedNotRegistered(feedId);

        uint256 current = feed.currentRoundId;
        if (current != 0 && _isLive(_rounds[current].state)) {
            revert RoundAlreadyOpen(feedId, current);
        }

        uint256 eligible = _staking.activeNodeCount();
        if (eligible < _minQuorumNodes) revert NotEnoughEligibleNodes(eligible, _minQuorumNodes);

        roundId = _nextRoundId++;
        uint256 version = _staking.nodeSetVersion();
        uint64 openedAt = uint64(block.timestamp);
        uint64 deadline = uint64(block.timestamp + _roundDuration);

        _rounds[roundId] = Round({
            feedId: feedId,
            openedAt: openedAt,
            deadline: deadline,
            settledAt: 0,
            state: RoundState.OPEN,
            nodeSetVersion: version,
            eligibleCount: eligible,
            aggregatedValue: 0
        });
        feed.currentRoundId = roundId;

        emit RoundStarted(roundId, feedId, openedAt, deadline, eligible, version);
    }

    /// @inheritdoc IOracleRounds
    /// @dev msg.sender must be the node, so submission gas stays on the node and spam is
    ///      self-limiting. The signature is not authentication — it is a portable attestation the
    ///      aggregation service verifies independently of transaction provenance.
    function submit(
        uint256 roundId,
        uint256 value,
        uint256 nonce,
        bytes calldata signature
    ) external whenNotPaused {
        Round storage round = _rounds[roundId];
        if (!_isLive(round.state)) revert RoundNotOpen(roundId);
        // slither-disable-next-line timestamp
        if (block.timestamp > round.deadline) revert RoundNotOpen(roundId);
        if (value == 0) revert ZeroValue();

        // Eligibility is judged against the snapshot, not the live set: a node that joined after
        // the round opened is not in the denominator and must not add to the numerator.
        if (!_staking.isEligibleAt(msg.sender, round.nodeSetVersion)) {
            revert NotEligible(msg.sender, round.nodeSetVersion);
        }
        if (_submitted[roundId][msg.sender]) revert AlreadySubmitted(roundId, msg.sender);

        uint256 expected = _nonces[msg.sender];
        if (nonce != expected) revert InvalidNonce(nonce, expected);

        _verifySignature(roundId, round.feedId, value, msg.sender, nonce, signature);

        _nonces[msg.sender] = expected + 1;
        _submitted[roundId][msg.sender] = true;
        _roundValues[roundId].push(value);

        uint256 count = _roundValues[roundId].length;
        emit SubmissionReceived(roundId, msg.sender, value, count, signature);

        if (round.state == RoundState.OPEN && _hasQuorum(count, round.eligibleCount)) {
            round.state = RoundState.QUORUM_MET;
            emit RoundQuorumMet(roundId, count, round.eligibleCount);
        }
    }

    /// @inheritdoc IOracleRounds
    /// @dev Permissionless. A round with quorum can settle at any time; one without must wait for
    ///      its deadline, so late submissions still count.
    function settleRound(
        uint256 roundId
    ) external {
        Round storage round = _rounds[roundId];
        if (!_isLive(round.state)) revert RoundNotOpen(roundId);

        uint256 count = _roundValues[roundId].length;
        bool quorum = _hasQuorum(count, round.eligibleCount);

        if (!quorum) {
            // slither-disable-next-line timestamp
            if (block.timestamp <= round.deadline) revert RoundStillOpen(roundId, round.deadline);

            round.state = RoundState.FAILED;
            round.settledAt = uint64(block.timestamp);
            emit RoundFailed(roundId, round.feedId, count, round.eligibleCount);
            return;
        }

        uint256 median = _median(_roundValues[roundId]);

        round.state = RoundState.SETTLED;
        round.settledAt = uint64(block.timestamp);
        round.aggregatedValue = median;
        _feeds[round.feedId].lastSettledRoundId = roundId;

        emit RoundSettled(roundId, round.feedId, median, count);
    }

    // --- reader ---

    /// @inheritdoc IOracleReader
    function getValue(
        bytes32 feedId,
        uint256 maxStaleness
    ) external view returns (uint256 value, uint256 settledAt, uint256 roundId) {
        Feed storage feed = _feeds[feedId];
        if (!feed.registered) revert FeedNotRegistered(feedId);

        roundId = feed.lastSettledRoundId;
        if (roundId == 0) revert NoSettledRound(feedId);

        Round storage round = _rounds[roundId];
        settledAt = round.settledAt;
        // slither-disable-next-line timestamp
        if (block.timestamp - settledAt > maxStaleness) revert StaleValue(settledAt, maxStaleness);

        value = round.aggregatedValue;
    }

    // --- admin ---

    function registerFeed(
        bytes32 feedId,
        string calldata name,
        uint8 decimals
    ) external onlyRole(Roles.ORACLE_MANAGER_ROLE) {
        Feed storage feed = _feeds[feedId];
        if (feed.registered) revert FeedAlreadyRegistered(feedId);
        if (decimals > 38) revert InvalidDecimals(decimals);

        feed.registered = true;
        feed.decimals = decimals;
        emit FeedRegistered(feedId, name, decimals);
    }

    function deregisterFeed(
        bytes32 feedId
    ) external onlyRole(Roles.ORACLE_MANAGER_ROLE) {
        Feed storage feed = _feeds[feedId];
        if (!feed.registered) revert FeedNotRegistered(feedId);

        feed.registered = false;
        emit FeedDeregistered(feedId);
    }

    function setRoundDuration(
        uint256 value
    ) external onlyRole(Roles.ORACLE_MANAGER_ROLE) {
        if (value == 0) revert ZeroValue();
        emit RoundDurationUpdated(_roundDuration, value);
        _roundDuration = value;
    }

    function setQuorumBps(
        uint256 value
    ) external onlyRole(Roles.ORACLE_MANAGER_ROLE) {
        if (value == 0 || value > _BPS_DENOMINATOR) revert InvalidBps(value);
        emit QuorumBpsUpdated(_quorumBps, value);
        _quorumBps = value;
    }

    function setMinQuorumNodes(
        uint256 value
    ) external onlyRole(Roles.ORACLE_MANAGER_ROLE) {
        if (value == 0) revert ZeroValue();
        emit MinQuorumNodesUpdated(_minQuorumNodes, value);
        _minQuorumNodes = value;
    }

    function pause() external onlyRole(Roles.PAUSER_ROLE) {
        _pause();
    }

    function unpause() external onlyRole(Roles.PAUSER_ROLE) {
        _unpause();
    }

    // --- views ---

    function roundOf(
        uint256 roundId
    ) external view returns (Round memory) {
        return _rounds[roundId];
    }

    function submissionsOf(
        uint256 roundId
    ) external view returns (uint256[] memory) {
        return _roundValues[roundId];
    }

    function hasSubmitted(
        uint256 roundId,
        address node
    ) external view returns (bool) {
        return _submitted[roundId][node];
    }

    function nonceOf(
        address node
    ) external view returns (uint256) {
        return _nonces[node];
    }

    function currentRoundId(
        bytes32 feedId
    ) external view returns (uint256) {
        return _feeds[feedId].currentRoundId;
    }

    function lastSettledRoundId(
        bytes32 feedId
    ) external view returns (uint256) {
        return _feeds[feedId].lastSettledRoundId;
    }

    /// @notice Scale a feed's settled values are reported in, declared at registration.
    /// @dev The contract cannot enforce it — it medians whatever nodes submit — but one declared
    ///      value beats every consumer assuming 18 independently.
    function feedDecimals(
        bytes32 feedId
    ) external view returns (uint8) {
        Feed storage feed = _feeds[feedId];
        if (!feed.registered) revert FeedNotRegistered(feedId);
        return feed.decimals;
    }

    function isFeedRegistered(
        bytes32 feedId
    ) external view returns (bool) {
        return _feeds[feedId].registered;
    }

    function staking() external view returns (address) {
        return address(_staking);
    }

    function roundDuration() external view returns (uint256) {
        return _roundDuration;
    }

    function quorumBps() external view returns (uint256) {
        return _quorumBps;
    }

    function minQuorumNodes() external view returns (uint256) {
        return _minQuorumNodes;
    }

    function maxSubmissions() external pure returns (uint256) {
        return _MAX_SUBMISSIONS;
    }

    /// @notice EIP-712 digest a node signs. Exposed so node operators can verify off-chain.
    function submissionDigest(
        uint256 roundId,
        bytes32 feedId,
        uint256 value,
        address node,
        uint256 nonce
    ) public view returns (bytes32) {
        return
            _hashTypedDataV4(keccak256(abi.encode(_SUBMISSION_TYPEHASH, roundId, feedId, value, node, nonce)));
    }

    // --- internals ---

    function _verifySignature(
        uint256 roundId,
        bytes32 feedId,
        uint256 value,
        address node,
        uint256 nonce,
        bytes calldata signature
    ) private view {
        bytes32 digest = submissionDigest(roundId, feedId, value, node, nonce);
        address recovered = ECDSA.recover(digest, signature);
        if (recovered != node) revert InvalidSignature(node);
    }

    function _hasQuorum(
        uint256 count,
        uint256 eligible
    ) private view returns (bool) {
        if (count < _minQuorumNodes) return false;
        return count * _BPS_DENOMINATOR >= eligible * _quorumBps;
    }

    function _isLive(
        RoundState state
    ) private pure returns (bool) {
        return state == RoundState.OPEN || state == RoundState.QUORUM_MET;
    }

    /// @dev Insertion sort over a memory copy. Bounded by the node set size, which the staking
    ///      contract caps, so this is not an unbounded loop over user-supplied data.
    function _median(
        uint256[] storage values
    ) private view returns (uint256) {
        uint256 length = values.length;
        uint256[] memory sorted = new uint256[](length);
        for (uint256 i = 0; i < length; i++) {
            sorted[i] = values[i];
        }

        for (uint256 i = 1; i < length; i++) {
            uint256 key = sorted[i];
            uint256 j = i;
            while (j > 0 && sorted[j - 1] > key) {
                sorted[j] = sorted[j - 1];
                unchecked {
                    --j;
                }
            }
            sorted[j] = key;
        }

        uint256 mid = length / 2;
        if (length % 2 == 1) return sorted[mid];
        return Math.average(sorted[mid - 1], sorted[mid]);
    }

    function _authorizeUpgrade(
        address
    ) internal override onlyRole(Roles.UPGRADER_ROLE) {}
}
