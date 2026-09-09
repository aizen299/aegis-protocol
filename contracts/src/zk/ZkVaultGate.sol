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

import {Roles} from "../shared/access/Roles.sol";
import {ICommitmentTree} from "./interfaces/ICommitmentTree.sol";
import {IZkVaultGate} from "./interfaces/IZkVaultGate.sol";
import {IZkVerifier} from "./verifier/IZkVerifier.sol";

/// @title ZkVaultGate (v0.4)
/// @notice Proves membership in the commitment tree and spends a nullifier, without revealing which
///         commitment was used.
/// @dev The gate is where replay is actually rejected. The circuit cannot do it — it has no memory.
///      What the circuit guarantees is that the same secret and domain always produce the same
///      nullifier, which is precisely what lets this map see a repeat. See
///      docs/v0.4-zk-plan.md §2.3.
///
///      Storage layout is append-only. Run `make contracts-layout-check` before any upgrade.
contract ZkVaultGate is
    Initializable,
    UUPSUpgradeable,
    AccessControlUpgradeable,
    ReentrancyGuardUpgradeable,
    IZkVaultGate
{
    /// @dev The BN254 scalar field. Every public input must be a field element: the verifier
    ///      rejects anything at or above this, so a value that exceeds it can never be proved
    ///      against. keccak256 output exceeds it for roughly one identifier in nine, which makes
    ///      this a live footgun rather than a theoretical one.
    uint256 internal constant FIELD_SIZE =
        21888242871839275222246405745257275088548364400416034343698204186575808495617;

    // --- storage (append-only) ---
    IZkVerifier private _verifier;
    ICommitmentTree private _tree;
    mapping(uint256 chainId => mapping(bytes32 nullifier => bool spent)) private _spent;
    mapping(bytes32 actionId => bool registered) private _actions;

    // slither-disable-next-line unused-state
    uint256[40] private __gap;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(
        address admin,
        address verifier_,
        address tree_
    ) external initializer {
        if (admin == address(0) || verifier_ == address(0) || tree_ == address(0)) {
            revert ZeroAddress();
        }

        __UUPSUpgradeable_init();
        __AccessControl_init();
        __ReentrancyGuard_init();

        _verifier = IZkVerifier(verifier_);
        _tree = ICommitmentTree(tree_);

        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    /// @inheritdoc IZkVaultGate
    /// @dev The chain and this contract's own address are read here, never taken from the caller.
    ///      A caller who could supply them could claim any domain, which would make the separation
    ///      advisory rather than enforced.
    function executePrivateAction(
        bytes calldata proof,
        bytes32 root,
        bytes32 nullifier,
        bytes32 actionId
    ) external nonReentrant {
        if (!_actions[actionId]) revert UnknownAction(actionId);

        // Any root in the tree's window, not only the newest: a deposit landing between reading the
        // tree and submitting would otherwise invalidate an honest proof.
        if (!_tree.isKnownRoot(root)) revert UnknownRoot(root);

        // Chain-scoped, per §2.5. Phase 2 deploys independently per chain, and a map that is not
        // chain-scoped lets a proof spent on one chain be replayed on another.
        if (uint256(nullifier) >= FIELD_SIZE) revert NotAFieldElement(nullifier);
        if (_spent[block.chainid][nullifier]) revert NullifierAlreadySpent(nullifier);

        bytes32[] memory publicInputs = new bytes32[](5);
        publicInputs[0] = root;
        publicInputs[1] = nullifier;
        publicInputs[2] = actionId;
        publicInputs[3] = bytes32(block.chainid);
        publicInputs[4] = bytes32(uint256(uint160(address(this))));

        if (!_verifier.verify(proof, publicInputs)) revert InvalidProof();

        // Spent before anything else can observe the gate: a reentrant call must not find the
        // nullifier unspent.
        _spent[block.chainid][nullifier] = true;

        emit PrivateActionExecuted(nullifier, actionId, root, block.chainid);
    }

    // --- actions ---

    /// @dev Actions are registered explicitly rather than accepted from calldata, so the set of
    ///      domains a proof can be presented against is enumerable and bounded.
    function registerAction(
        bytes32 actionId,
        string calldata name
    ) external onlyRole(Roles.ZK_GATE_MANAGER_ROLE) {
        if (bytes(name).length == 0) revert EmptyActionName();
        // Rejected here rather than at execution: an action nobody can ever prove against is a
        // configuration error, and it should fail when it is made, not when a user first tries.
        if (uint256(actionId) >= FIELD_SIZE) revert NotAFieldElement(actionId);
        if (_actions[actionId]) revert ActionAlreadyRegistered(actionId);

        _actions[actionId] = true;
        emit ActionRegistered(actionId, name);
    }

    /// @dev Deregistering stops new proofs for an action. It does not un-spend nullifiers: those
    ///      stay spent, so re-registering cannot resurrect a used proof.
    function deregisterAction(
        bytes32 actionId
    ) external onlyRole(Roles.ZK_GATE_MANAGER_ROLE) {
        if (!_actions[actionId]) revert UnknownAction(actionId);

        _actions[actionId] = false;
        emit ActionDeregistered(actionId);
    }

    // --- views ---

    /// @inheritdoc IZkVaultGate
    function isSpent(
        bytes32 nullifier
    ) external view returns (bool) {
        return _spent[block.chainid][nullifier];
    }

    /// @inheritdoc IZkVaultGate
    function isActionRegistered(
        bytes32 actionId
    ) external view returns (bool) {
        return _actions[actionId];
    }

    function verifier() external view returns (address) {
        return address(_verifier);
    }

    function tree() external view returns (address) {
        return address(_tree);
    }

    function _authorizeUpgrade(
        address
    ) internal override onlyRole(Roles.UPGRADER_ROLE) {}
}
