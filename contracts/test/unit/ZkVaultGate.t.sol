// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";

import {HonkVerifier} from "../../generated/HonkVerifier.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {ZkVaultGate} from "../../src/zk/ZkVaultGate.sol";
import {IZkVaultGate} from "../../src/zk/interfaces/IZkVaultGate.sol";
import {CommitmentTreeFixture} from "../utils/CommitmentTreeFixture.sol";
import {ProofFixture} from "../utils/ProofFixture.sol";

/// @title The gate is where replay is rejected.
/// @dev The circuit cannot reject a reused nullifier — it has no memory. It guarantees the same
///      secret and domain always produce the same nullifier, and this is the map that sees the
///      repeat. That test has been owed since the circuit was written.
contract ZkVaultGateTest is CommitmentTreeFixture {
    address internal manager = makeAddr("manager");

    ZkVaultGate internal gate;
    bytes32 internal actionId;

    function setUp() public override {
        super.setUp();

        gate = _deployGateAtAFixedAddress(_deployVerifier(), admin, address(tree));

        // The proof binds the gate's address and the chain. If either drifts, no proof in this
        // suite can verify, and the failure would otherwise look like a broken verifier.
        assertEq(
            uint256(uint160(address(gate))),
            uint256(ProofFixture.GATE),
            "gate address changed; regenerate with GATE=<addr> make zk-proof-fixture"
        );
        assertEq(block.chainid, uint256(ProofFixture.CHAIN_ID), "chain id differs from the fixture");

        actionId = ProofFixture.ACTION_ID;

        vm.startPrank(admin);
        gate.grantRole(Roles.ZK_GATE_MANAGER_ROLE, manager);
        gate.grantRole(Roles.UPGRADER_ROLE, upgrader);
        vm.stopPrank();

        vm.prank(manager);
        gate.registerAction(actionId, "vault-membership");

        // The commitment the fixture's proof was built for, at leaf 0 of an otherwise-empty tree.
        _insert(_commitmentOf(424242));
    }

    // --- the root the JS, the circuit, and the contract all have to agree on ---

    /// The witness was built by circomlibjs modelling this contract's zero subtrees. If the two
    /// disagree, the proof is for a tree that does not exist on chain.
    function test_theTreeRootMatchesTheOneTheProofWasBuiltFor() public view {
        assertEq(tree.currentRoot(), ProofFixture.ROOT, "the contract and the witness disagree");
    }

    // --- the whole point ---

    function test_aValidProofExecutesTheAction() public {
        vm.expectEmit(true, true, false, true, address(gate));
        emit IZkVaultGate.PrivateActionExecuted(
            ProofFixture.NULLIFIER_HASH, actionId, ProofFixture.ROOT, block.chainid
        );

        gate.executePrivateAction(
            ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, actionId
        );

        assertTrue(gate.isSpent(ProofFixture.NULLIFIER_HASH), "the nullifier was not spent");
    }

    /// The test the circuit could not provide.
    function test_aReplayedNullifierIsRejected() public {
        gate.executePrivateAction(
            ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, actionId
        );

        vm.expectRevert(
            abi.encodeWithSelector(IZkVaultGate.NullifierAlreadySpent.selector, ProofFixture.NULLIFIER_HASH)
        );
        gate.executePrivateAction(
            ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, actionId
        );
    }

    /// A replay must be refused before the proof is even checked, so a valid proof cannot be
    /// resubmitted by paying for verification twice.
    function test_aReplayIsRejectedBeforeVerification() public {
        gate.executePrivateAction(
            ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, actionId
        );

        uint256 gasBefore = gasleft();
        try gate.executePrivateAction(
            ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, actionId
        ) {
            revert("the replay succeeded");
        } catch {}
        uint256 used = gasBefore - gasleft();

        // A full verification costs millions of gas; the rejection must be nowhere near that.
        assertLt(used, 200_000, "the replay ran the verifier before refusing");
    }

    // --- domain separation ---

    function test_anUnregisteredActionIsRejected() public {
        bytes32 other = keccak256("some-other-action");

        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.UnknownAction.selector, other));
        gate.executePrivateAction(ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, other);
    }

    /// A proof for one action must not pass at another, even when both are registered. This is what
    /// makes the gate action-specific rather than a single global authorisation.
    function test_aProofFromAnotherActionDoesNotPass() public {
        bytes32 other = _fieldElement("withdraw");
        vm.prank(manager);
        gate.registerAction(other, "withdraw");

        // The verifier reverts with its own error rather than returning false on some paths, so the
        // assertion that matters is the state: nothing was spent and no action took effect.
        vm.expectRevert();
        gate.executePrivateAction(ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, other);

        assertFalse(gate.isSpent(ProofFixture.NULLIFIER_HASH), "a cross-action proof spent a nullifier");
    }

    /// keccak256 output exceeds the BN254 field modulus for roughly one identifier in nine, and the
    /// verifier cannot accept a public input that is not a field element. Registering such an action
    /// would create one nobody could ever prove against.
    function test_anActionIdOutsideTheFieldIsRejected() public {
        bytes32 tooLarge = bytes32(type(uint256).max);

        vm.prank(manager);
        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.NotAFieldElement.selector, tooLarge));
        gate.registerAction(tooLarge, "unprovable");
    }

    function test_aNullifierOutsideTheFieldIsRejected() public {
        bytes32 tooLarge = bytes32(type(uint256).max);

        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.NotAFieldElement.selector, tooLarge));
        gate.executePrivateAction(ProofFixture.PROOF, ProofFixture.ROOT, tooLarge, actionId);
    }

    /// @dev Reduces a label into the scalar field, which is what any real action id must do.
    function _fieldElement(
        string memory label
    ) internal pure returns (bytes32) {
        uint256 fieldSize = 21888242871839275222246405745257275088548364400416034343698204186575808495617;
        return bytes32(uint256(keccak256(bytes(label))) % fieldSize);
    }

    function test_aRootTheTreeNeverHeldIsRejected() public {
        bytes32 invented = bytes32(uint256(ProofFixture.ROOT) + 1);

        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.UnknownRoot.selector, invented));
        gate.executePrivateAction(ProofFixture.PROOF, invented, ProofFixture.NULLIFIER_HASH, actionId);
    }

    /// The root is checked against the tree's window, so a proof stays valid while later deposits
    /// land — the reason the window exists at all.
    function test_aProofStaysValidWhileLaterDepositsLand() public {
        _insert(_commitmentOf(999));
        _insert(_commitmentOf(1000));

        assertTrue(tree.currentRoot() != ProofFixture.ROOT, "the tree did not move");

        gate.executePrivateAction(
            ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, actionId
        );
        assertTrue(gate.isSpent(ProofFixture.NULLIFIER_HASH));
    }

    function test_aForgedNullifierIsRejected() public {
        bytes32 forged = bytes32(uint256(ProofFixture.NULLIFIER_HASH) + 1);

        vm.expectRevert();
        gate.executePrivateAction(ProofFixture.PROOF, ProofFixture.ROOT, forged, actionId);

        assertFalse(gate.isSpent(forged), "a forged nullifier was spent");
        assertFalse(gate.isSpent(ProofFixture.NULLIFIER_HASH), "the real nullifier was spent");
    }

    function test_aTamperedProofIsRejected() public {
        bytes memory tampered = ProofFixture.PROOF;
        tampered[128] = bytes1(uint8(tampered[128]) ^ 0xff);

        vm.expectRevert();
        gate.executePrivateAction(tampered, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, actionId);

        assertFalse(gate.isSpent(ProofFixture.NULLIFIER_HASH), "a tampered proof spent a nullifier");
    }

    // --- action registry ---

    function test_deregisteringStopsNewProofsButKeepsNullifiersSpent() public {
        gate.executePrivateAction(
            ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, actionId
        );

        vm.prank(manager);
        gate.deregisterAction(actionId);
        assertFalse(gate.isActionRegistered(actionId));

        // Re-registering must not resurrect a used proof.
        vm.prank(manager);
        gate.registerAction(actionId, "vault-membership");

        vm.expectRevert(
            abi.encodeWithSelector(IZkVaultGate.NullifierAlreadySpent.selector, ProofFixture.NULLIFIER_HASH)
        );
        gate.executePrivateAction(
            ProofFixture.PROOF, ProofFixture.ROOT, ProofFixture.NULLIFIER_HASH, actionId
        );
    }

    function test_onlyTheManagerCanRegisterActions() public {
        bytes32 other = keccak256("other");

        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, outsider, Roles.ZK_GATE_MANAGER_ROLE
            )
        );
        gate.registerAction(other, "other");
    }

    function test_anActionCannotBeRegisteredTwiceOrWithoutAName() public {
        vm.startPrank(manager);

        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.ActionAlreadyRegistered.selector, actionId));
        gate.registerAction(actionId, "vault-membership");

        vm.expectRevert(IZkVaultGate.EmptyActionName.selector);
        gate.registerAction(keccak256("nameless"), "");

        vm.stopPrank();
    }
}
