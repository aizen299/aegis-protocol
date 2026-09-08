// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice Round lifecycle and submission surface of the oracle network (v0.2).
interface IOracleRounds {
    enum RoundState {
        NONE,
        OPEN,
        QUORUM_MET,
        SETTLED,
        FAILED
    }

    /// @dev Part of the read surface, so it lives on the interface rather than the implementation.
    ///      `nodeSetVersion` and `eligibleCount` are the snapshot taken at open: quorum is judged
    ///      against the set that existed then, not the live one.
    struct Round {
        bytes32 feedId;
        uint64 openedAt;
        uint64 deadline;
        uint64 settledAt;
        RoundState state;
        uint256 nodeSetVersion;
        uint256 eligibleCount;
        uint256 aggregatedValue;
    }

    event FeedRegistered(bytes32 indexed feedId, string name, uint8 decimals);
    event FeedDeregistered(bytes32 indexed feedId);
    event RoundStarted(
        uint256 indexed roundId,
        bytes32 indexed feedId,
        uint256 openedAt,
        uint256 deadline,
        uint256 eligibleCount,
        uint256 nodeSetVersion
    );
    /// @dev The nonce is emitted because without it the signature cannot be verified off-chain, and
    ///      an unverifiable signature in the log is decorative. The aggregation service checks these
    ///      independently of the contract, which is the point of storing them at all.
    event SubmissionReceived(
        uint256 indexed roundId,
        address indexed node,
        uint256 value,
        uint256 nonce,
        uint256 submissionCount,
        bytes signature
    );
    event RoundQuorumMet(uint256 indexed roundId, uint256 submissionCount, uint256 eligibleCount);
    event RoundSettled(
        uint256 indexed roundId, bytes32 indexed feedId, uint256 aggregatedValue, uint256 submissionCount
    );
    event RoundFailed(
        uint256 indexed roundId, bytes32 indexed feedId, uint256 submissionCount, uint256 eligibleCount
    );

    event RoundDurationUpdated(uint256 previousValue, uint256 newValue);
    event QuorumBpsUpdated(uint256 previousValue, uint256 newValue);
    event MinQuorumNodesUpdated(uint256 previousValue, uint256 newValue);

    error ZeroAddress();
    error ZeroValue();
    error FeedNotRegistered(bytes32 feedId);
    error FeedAlreadyRegistered(bytes32 feedId);
    error RoundNotOpen(uint256 roundId);
    error RoundStillOpen(uint256 roundId, uint256 deadline);
    error RoundAlreadyOpen(bytes32 feedId, uint256 roundId);
    error NotEligible(address node, uint256 nodeSetVersion);
    error AlreadySubmitted(uint256 roundId, address node);
    error InvalidSignature(address node);
    error InvalidNonce(uint256 provided, uint256 expected);
    error NotEnoughEligibleNodes(uint256 eligible, uint256 required);
    error InvalidBps(uint256 bps);
    error InvalidDecimals(uint8 decimals);
    error NoSettledRound(bytes32 feedId);
    error StaleValue(uint256 settledAt, uint256 maxStaleness);

    function openRound(
        bytes32 feedId
    ) external returns (uint256 roundId);
    function submit(
        uint256 roundId,
        uint256 value,
        uint256 nonce,
        bytes calldata signature
    ) external;
    function settleRound(
        uint256 roundId
    ) external;
}

/// @notice Read surface for downstream modules.
/// @dev There is deliberately no `latestAnswer()`. A getter that returns a price without forcing
///      the caller to state a freshness bound is how oracle consumers get exploited: the value
///      looks fine and is hours old. `maxStaleness` has no default.
interface IOracleReader {
    function getValue(
        bytes32 feedId,
        uint256 maxStaleness
    ) external view returns (uint256 value, uint256 settledAt, uint256 roundId);
}
