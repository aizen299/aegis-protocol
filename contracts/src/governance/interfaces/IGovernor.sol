// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice Proposal, voting, and execution surface of the DAO (v0.3).
/// @dev Events here are the backend's entire view of governance; changing them breaks
///      `backend/internal/indexer`.
interface IGovernor {
    /// @dev `dispatched` is not terminal and is unreachable in Phase 1. It exists so that adding
    ///      cross-chain execution later is not a migration of the state machine — see
    ///      docs/project-spec.md §7.
    enum ProposalState {
        NONE,
        PENDING,
        ACTIVE,
        SUCCEEDED,
        DEFEATED,
        QUEUED,
        DISPATCHED,
        EXECUTED,
        FAILED,
        CANCELLED
    }

    enum Support {
        AGAINST,
        FOR,
        ABSTAIN
    }

    /// @dev The target is 32 bytes, not an address. A cross-chain destination is a
    ///      (chain, program, payload) triple and a Solana program is 32 bytes; typing this as an
    ///      address would bake a local EVM target into the one structure §7 says must not assume
    ///      one. An EVM address occupies the low 20 bytes.
    struct Action {
        uint256 targetChainId;
        bytes32 target;
        uint256 value;
        bytes payload;
    }

    struct Proposal {
        address proposer;
        uint48 voteStart;
        uint48 voteEnd;
        uint48 executableAt;
        ProposalState state;
        uint256 forVotes;
        uint256 againstVotes;
        uint256 abstainVotes;
        Action action;
    }

    /// @dev Carries the whole action because the indexer must reconstruct what a proposal does
    ///      without a contract read — the same rule that made the oracle emit its nonce.
    event ProposalCreated(
        uint256 indexed proposalId,
        address indexed proposer,
        uint256 targetChainId,
        bytes32 target,
        uint256 value,
        bytes payload,
        uint256 voteStart,
        uint256 voteEnd,
        string title,
        string description
    );
    event VoteCast(
        uint256 indexed proposalId, address indexed voter, uint8 support, uint256 weight, string reason
    );
    event ProposalQueued(uint256 indexed proposalId, uint256 executableAt);
    event ProposalExecuted(uint256 indexed proposalId);
    event ProposalDispatched(uint256 indexed proposalId, uint256 indexed targetChainId);
    event ProposalCancelled(uint256 indexed proposalId);

    event VotingDelayUpdated(uint256 previousValue, uint256 newValue);
    event VotingPeriodUpdated(uint256 previousValue, uint256 newValue);
    event ProposalThresholdUpdated(uint256 previousValue, uint256 newValue);
    event QuorumNumeratorUpdated(uint256 previousValue, uint256 newValue);
    event TimelockDelayUpdated(uint256 previousValue, uint256 newValue);

    error ZeroAddress();
    error EmptyTitle();
    error UnknownProposal(uint256 proposalId);
    error BelowProposalThreshold(uint256 weight, uint256 threshold);
    error ProposalNotActive(uint256 proposalId, ProposalState state);
    error ProposalNotSucceeded(uint256 proposalId, ProposalState state);
    error ProposalNotQueued(uint256 proposalId, ProposalState state);
    error AlreadyVoted(uint256 proposalId, address voter);
    error NoVotingPower(address voter);
    error InvalidSupport(uint8 support);
    error VotingNotFinished(uint256 proposalId, uint256 voteEnd);
    error TimelockNotElapsed(uint256 executableAt, uint256 nowTimestamp);
    error NotProposerOrGuardian(address caller);
    error ProposalNotCancellable(uint256 proposalId, ProposalState state);
    error TargetNotLocalAddress(bytes32 target);
    error CrossChainDispatchUnavailable(uint256 targetChainId);
    error InvalidQuorumNumerator(uint256 numerator);
    error ZeroValue();
    error ExecutionReverted(uint256 proposalId);

    function propose(
        Action calldata action,
        string calldata title,
        string calldata description
    ) external returns (uint256 proposalId);
    function castVote(
        uint256 proposalId,
        uint8 support,
        string calldata reason
    ) external;
    function queue(
        uint256 proposalId
    ) external;
    function execute(
        uint256 proposalId
    ) external;
    function cancel(
        uint256 proposalId
    ) external;

    function state(
        uint256 proposalId
    ) external view returns (ProposalState);
    function proposalOf(
        uint256 proposalId
    ) external view returns (Proposal memory);
}
