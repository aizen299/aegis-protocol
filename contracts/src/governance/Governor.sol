// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {
    AccessControlUpgradeable
} from "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";
import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import {
    ReentrancyGuardUpgradeable
} from "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";
import {IVotes} from "@openzeppelin/contracts/governance/utils/IVotes.sol";
import {Math} from "@openzeppelin/contracts/utils/math/Math.sol";

import {Roles} from "../shared/access/Roles.sol";
import {IGovernor} from "./interfaces/IGovernor.sol";
import {ITimelock} from "./interfaces/ITimelock.sol";

/// @title Governor (v0.3)
/// @notice Proposal creation, voting on snapshotted weight, and queueing into the timelock.
/// @dev This contract decides; the Timelock waits and calls. Protocol roles are held by the
///      Timelock, so replacing a governor is a role change on one contract rather than a
///      migration of every role in the protocol.
/// @dev Storage layout is append-only. Run `make contracts-layout-check` before any upgrade.
contract Governor is
    Initializable,
    UUPSUpgradeable,
    AccessControlUpgradeable,
    ReentrancyGuardUpgradeable,
    IGovernor
{
    uint256 private constant _QUORUM_DENOMINATOR = 100;

    // --- storage (append-only) ---
    IVotes private _token;
    uint48 private _votingDelay;
    uint48 private _votingPeriod;
    ITimelock private _timelock;
    uint256 private _proposalThreshold;
    uint256 private _quorumNumerator;
    uint256 private _proposalCount;
    mapping(uint256 proposalId => Proposal proposal) private _proposals;
    mapping(uint256 proposalId => mapping(address voter => bool voted)) private _hasVoted;

    // slither-disable-next-line unused-state
    uint256[40] private __gap;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(
        address admin,
        address token_,
        address timelock_,
        uint48 votingDelay_,
        uint48 votingPeriod_,
        uint256 proposalThreshold_,
        uint256 quorumNumerator_
    ) external initializer {
        if (admin == address(0) || token_ == address(0) || timelock_ == address(0)) {
            revert ZeroAddress();
        }
        if (votingPeriod_ == 0) revert ZeroValue();
        if (quorumNumerator_ == 0 || quorumNumerator_ > _QUORUM_DENOMINATOR) {
            revert InvalidQuorumNumerator(quorumNumerator_);
        }

        __UUPSUpgradeable_init();
        __AccessControl_init();
        __ReentrancyGuard_init();

        _token = IVotes(token_);
        _timelock = ITimelock(payable(timelock_));
        _votingDelay = votingDelay_;
        _votingPeriod = votingPeriod_;
        _proposalThreshold = proposalThreshold_;
        _quorumNumerator = quorumNumerator_;

        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    // --- proposals ---

    /// @inheritdoc IGovernor
    /// @dev Weight is checked against the proposer's power now; the vote snapshot is taken at
    ///      voteStart. A proposer must already hold influence to spend gas on everyone else.
    function propose(
        Action calldata action,
        string calldata title,
        string calldata description
    ) external returns (uint256 proposalId) {
        if (bytes(title).length == 0) revert EmptyTitle();

        uint256 weight = _token.getVotes(msg.sender);
        if (weight < _proposalThreshold) revert BelowProposalThreshold(weight, _proposalThreshold);

        uint48 voteStart = uint48(block.timestamp) + _votingDelay;
        uint48 voteEnd = voteStart + _votingPeriod;

        proposalId = ++_proposalCount;
        Proposal storage proposal = _proposals[proposalId];
        proposal.proposer = msg.sender;
        proposal.voteStart = voteStart;
        proposal.voteEnd = voteEnd;
        proposal.state = ProposalState.PENDING;
        proposal.action = action;

        // Emitted from its own frame: ProposalCreated carries the whole action so the indexer can
        // reconstruct a proposal without a contract read, and ten arguments plus this function's
        // locals overflow the stack when inlined.
        _emitCreated(proposalId, action, title, description, voteStart, voteEnd);
    }

    function _emitCreated(
        uint256 proposalId,
        Action calldata action,
        string calldata title,
        string calldata description,
        uint48 voteStart,
        uint48 voteEnd
    ) private {
        emit ProposalCreated(
            proposalId,
            msg.sender,
            action.targetChainId,
            action.target,
            action.value,
            action.payload,
            voteStart,
            voteEnd,
            title,
            description
        );
    }

    /// @inheritdoc IGovernor
    /// @dev Weight comes from the snapshot at voteStart, never from the current balance. A flash
    ///      loan cannot hold tokens across a block, so it cannot appear in a past checkpoint.
    function castVote(
        uint256 proposalId,
        uint8 support,
        string calldata reason
    ) external {
        Proposal storage proposal = _requireProposal(proposalId);

        ProposalState current = _liveState(proposalId, proposal);
        if (current != ProposalState.ACTIVE) revert ProposalNotActive(proposalId, current);
        if (support > uint8(Support.ABSTAIN)) revert InvalidSupport(support);
        if (_hasVoted[proposalId][msg.sender]) revert AlreadyVoted(proposalId, msg.sender);

        uint256 weight = _token.getPastVotes(msg.sender, proposal.voteStart);
        if (weight == 0) revert NoVotingPower(msg.sender);

        _hasVoted[proposalId][msg.sender] = true;

        if (support == uint8(Support.FOR)) {
            proposal.forVotes += weight;
        } else if (support == uint8(Support.AGAINST)) {
            proposal.againstVotes += weight;
        } else {
            proposal.abstainVotes += weight;
        }

        emit VoteCast(proposalId, msg.sender, support, weight, reason);
    }

    /// @inheritdoc IGovernor
    /// @dev Queueing writes the outcome to storage. Until then state() derives it, so a proposal
    ///      that nobody queues does not sit in storage claiming to have succeeded. The delay is
    ///      the timelock's, read back rather than recomputed, so there is one clock not two.
    function queue(
        uint256 proposalId
    ) external {
        Proposal storage proposal = _requireProposal(proposalId);

        ProposalState current = _liveState(proposalId, proposal);
        if (current != ProposalState.SUCCEEDED) revert ProposalNotSucceeded(proposalId, current);

        proposal.state = ProposalState.QUEUED;

        (uint256 operationId, uint48 executableAt) = _timelock.schedule(
            proposal.action.targetChainId,
            proposal.action.target,
            proposal.action.value,
            proposal.action.payload
        );
        proposal.operationId = operationId;
        proposal.executableAt = executableAt;

        // The event carries a value the call returns, so it cannot precede the call. The callee is
        // the timelock address fixed at initialization, and its schedule() makes no call of its own.
        // slither-disable-next-line reentrancy-events
        emit ProposalQueued(proposalId, operationId, executableAt);
    }

    /// @inheritdoc IGovernor
    /// @dev The call itself belongs to the timelock, which owns the delay, holds the value, and
    ///      branches on the destination chain. Anything that reverts there reverts here, so a
    ///      proposal is never recorded as executed when its action did not run.
    function execute(
        uint256 proposalId
    ) external nonReentrant {
        Proposal storage proposal = _requireProposal(proposalId);

        if (proposal.state != ProposalState.QUEUED) {
            revert ProposalNotQueued(proposalId, proposal.state);
        }

        // Marked executed before the call: a proposal that reenters must not find itself queued.
        proposal.state = ProposalState.EXECUTED;

        _timelock.execute(proposal.operationId);

        emit ProposalExecuted(proposalId);
    }

    /// @inheritdoc IGovernor
    /// @dev The proposer may withdraw their own proposal; the guardian may stop any that has not
    ///      executed. A guardian that could only act before a vote would be useless against the
    ///      case it exists for — a passed proposal nobody noticed was malicious.
    function cancel(
        uint256 proposalId
    ) external {
        Proposal storage proposal = _requireProposal(proposalId);

        bool isGuardian = hasRole(Roles.GOVERNANCE_GUARDIAN_ROLE, msg.sender);
        if (msg.sender != proposal.proposer && !isGuardian) revert NotProposerOrGuardian(msg.sender);

        ProposalState current = _liveState(proposalId, proposal);
        if (current == ProposalState.EXECUTED || current == ProposalState.CANCELLED) {
            revert ProposalNotCancellable(proposalId, current);
        }

        proposal.state = ProposalState.CANCELLED;
        emit ProposalCancelled(proposalId);

        // A cancelled proposal that left a live operation behind would still be executable by
        // anything else holding the executor role. The queue has to be cleared too.
        if (current == ProposalState.QUEUED) {
            _timelock.cancel(proposal.operationId);
        }
    }

    // --- views ---

    /// @inheritdoc IGovernor
    function state(
        uint256 proposalId
    ) public view returns (ProposalState) {
        Proposal storage proposal = _proposals[proposalId];
        if (proposal.proposer == address(0)) return ProposalState.NONE;
        return _liveState(proposalId, proposal);
    }

    /// @inheritdoc IGovernor
    function proposalOf(
        uint256 proposalId
    ) external view returns (Proposal memory) {
        return _proposals[proposalId];
    }

    /// @notice Votes required for a proposal to pass, as of a snapshot.
    /// @dev A fraction of the supply that existed at the snapshot, not of today's supply, so the
    ///      bar cannot move under a vote that is already running.
    function quorum(
        uint256 timepoint
    ) public view returns (uint256) {
        return Math.mulDiv(_token.getPastTotalSupply(timepoint), _quorumNumerator, _QUORUM_DENOMINATOR);
    }

    function hasVoted(
        uint256 proposalId,
        address voter
    ) external view returns (bool) {
        return _hasVoted[proposalId][voter];
    }

    function token() external view returns (address) {
        return address(_token);
    }

    function votingDelay() external view returns (uint256) {
        return _votingDelay;
    }

    function votingPeriod() external view returns (uint256) {
        return _votingPeriod;
    }

    function timelock() external view returns (address) {
        return address(_timelock);
    }

    function proposalThreshold() external view returns (uint256) {
        return _proposalThreshold;
    }

    function quorumNumerator() external view returns (uint256) {
        return _quorumNumerator;
    }

    function proposalCount() external view returns (uint256) {
        return _proposalCount;
    }

    // --- parameters ---
    //
    // Gated on DEFAULT_ADMIN_ROLE, which the migration in docs/v0.3-governance-plan.md §2.4 hands
    // to this contract itself once it is trusted. Until then a multisig holds it.

    function setVotingDelay(
        uint48 value
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        emit VotingDelayUpdated(_votingDelay, value);
        _votingDelay = value;
    }

    function setVotingPeriod(
        uint48 value
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        if (value == 0) revert ZeroValue();
        emit VotingPeriodUpdated(_votingPeriod, value);
        _votingPeriod = value;
    }

    function setProposalThreshold(
        uint256 value
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        emit ProposalThresholdUpdated(_proposalThreshold, value);
        _proposalThreshold = value;
    }

    function setQuorumNumerator(
        uint256 value
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        if (value == 0 || value > _QUORUM_DENOMINATOR) revert InvalidQuorumNumerator(value);
        emit QuorumNumeratorUpdated(_quorumNumerator, value);
        _quorumNumerator = value;
    }

    // --- internals ---

    /// @dev Derives the outcome of a proposal whose vote has ended but which nobody has queued.
    ///      Stored states win: once queued, executed, or cancelled, the record is authoritative.
    function _liveState(
        uint256 proposalId,
        Proposal storage proposal
    ) private view returns (ProposalState) {
        ProposalState stored = proposal.state;
        if (
            stored == ProposalState.QUEUED || stored == ProposalState.EXECUTED
                || stored == ProposalState.CANCELLED || stored == ProposalState.DISPATCHED
                || stored == ProposalState.FAILED
        ) {
            return stored;
        }

        // Voting opens the second *after* the snapshot, not on it. `getPastVotes` refuses a
        // timepoint that is not yet in the past, so reporting ACTIVE at exactly voteStart would
        // advertise a window in which every vote reverts. Found by the invariant suite.
        // slither-disable-next-line timestamp
        if (block.timestamp <= proposal.voteStart) return ProposalState.PENDING;
        // slither-disable-next-line timestamp
        if (block.timestamp <= proposal.voteEnd) return ProposalState.ACTIVE;

        return _outcome(proposalId, proposal);
    }

    /// @dev A proposal passes on more for than against AND reaching quorum. Abstentions count
    ///      toward quorum but not toward the margin, which is what makes abstaining meaningful
    ///      rather than equivalent to not voting.
    function _outcome(
        uint256,
        Proposal storage proposal
    ) private view returns (ProposalState) {
        uint256 participation = proposal.forVotes + proposal.againstVotes + proposal.abstainVotes;
        if (participation < quorum(proposal.voteStart)) return ProposalState.DEFEATED;
        if (proposal.forVotes <= proposal.againstVotes) return ProposalState.DEFEATED;

        return ProposalState.SUCCEEDED;
    }

    function _requireProposal(
        uint256 proposalId
    ) private view returns (Proposal storage) {
        Proposal storage proposal = _proposals[proposalId];
        if (proposal.proposer == address(0)) revert UnknownProposal(proposalId);
        return proposal;
    }

    function _authorizeUpgrade(
        address
    ) internal override onlyRole(Roles.UPGRADER_ROLE) {}
}
