// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice Protocol-wide role identifiers. Shared so every module and the backend agree on hashes.
library Roles {
    bytes32 internal constant UPGRADER_ROLE = keccak256("UPGRADER_ROLE");
    bytes32 internal constant PAUSER_ROLE = keccak256("PAUSER_ROLE");
    bytes32 internal constant VAULT_MANAGER_ROLE = keccak256("VAULT_MANAGER_ROLE");
    bytes32 internal constant ORACLE_ROLE = keccak256("ORACLE_ROLE");
    bytes32 internal constant ORACLE_MANAGER_ROLE = keccak256("ORACLE_MANAGER_ROLE");
    bytes32 internal constant SLASHER_ROLE = keccak256("SLASHER_ROLE");
    bytes32 internal constant GOVERNANCE_ROLE = keccak256("GOVERNANCE_ROLE");
    bytes32 internal constant GOVERNANCE_GUARDIAN_ROLE = keccak256("GOVERNANCE_GUARDIAN_ROLE");
    bytes32 internal constant TIMELOCK_PROPOSER_ROLE = keccak256("TIMELOCK_PROPOSER_ROLE");
    bytes32 internal constant TIMELOCK_EXECUTOR_ROLE = keccak256("TIMELOCK_EXECUTOR_ROLE");
    bytes32 internal constant TIMELOCK_CANCELLER_ROLE = keccak256("TIMELOCK_CANCELLER_ROLE");
    bytes32 internal constant COMMITMENT_WRITER_ROLE = keccak256("COMMITMENT_WRITER_ROLE");
}
