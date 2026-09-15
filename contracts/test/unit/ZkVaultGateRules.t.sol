// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";

import {Roles} from "../../src/shared/access/Roles.sol";
import {ZkVaultGate} from "../../src/zk/ZkVaultGate.sol";
import {IZkVaultGate} from "../../src/zk/interfaces/IZkVaultGate.sol";
import {CommitmentTreeFixture} from "../utils/CommitmentTreeFixture.sol";

/// @title The gate's rules that hold without a valid proof.
/// @dev Deliberately separate from ZkVaultGateTest. That suite deploys the gate at the address the
///      committed proof binds, which coverage instrumentation moves, so it cannot run under
///      coverage. Everything here refuses before verification is ever reached, needs no proof, and
///      therefore no fixed address — which keeps most of this contract measurable.
contract ZkVaultGateRulesTest is CommitmentTreeFixture {
    uint256 internal constant FIELD_SIZE =
        21888242871839275222246405745257275088548364400416034343698204186575808495617;

    address internal manager = makeAddr("manager");

    ZkVaultGate internal gate;
    bytes32 internal actionId = bytes32(uint256(7));
    bytes internal emptyProof;

    /// @dev Cached in setUp. Reading it inside a test would be an external call in argument
    ///      position, which consumes the vm.expectRevert meant for the call after it.
    bytes32 internal knownRoot;

    function setUp() public override {
        super.setUp();

        gate = ZkVaultGate(
            address(
                new ERC1967Proxy(
                    address(new ZkVaultGate()),
                    abi.encodeCall(ZkVaultGate.initialize, (admin, _deployVerifier(), address(tree)))
                )
            )
        );

        vm.startPrank(admin);
        gate.grantRole(Roles.ZK_GATE_MANAGER_ROLE, manager);
        gate.grantRole(Roles.UPGRADER_ROLE, upgrader);
        vm.stopPrank();

        vm.prank(manager);
        gate.registerAction(actionId, "vault-membership");

        knownRoot = tree.currentRoot();
    }

    // --- refusals that precede verification ---

    function test_anUnregisteredActionIsRejected() public {
        bytes32 other = bytes32(uint256(8));

        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.UnknownAction.selector, other));
        gate.executePrivateAction(emptyProof, knownRoot, bytes32(uint256(1)), other);
    }

    function test_aRootTheTreeNeverHeldIsRejected() public {
        bytes32 invented = bytes32(uint256(123456));

        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.UnknownRoot.selector, invented));
        gate.executePrivateAction(emptyProof, invented, bytes32(uint256(1)), actionId);
    }

    /// keccak256 output exceeds the field modulus for roughly one identifier in nine, and the
    /// verifier cannot accept a public input that is not a field element.
    function test_aNullifierOutsideTheFieldIsRejected() public {
        bytes32 tooLarge = bytes32(FIELD_SIZE);

        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.NotAFieldElement.selector, tooLarge));
        gate.executePrivateAction(emptyProof, knownRoot, tooLarge, actionId);
    }

    function test_theLargestFieldElementIsAcceptedAsANullifier() public {
        // Reaches verification and fails there, which is the point: the field check let it through.
        bytes32 largest = bytes32(FIELD_SIZE - 1);

        vm.expectRevert();
        gate.executePrivateAction(emptyProof, knownRoot, largest, actionId);
        assertFalse(gate.isSpent(largest), "an unverified action spent a nullifier");
    }

    // --- the action registry ---

    function test_anActionIdOutsideTheFieldIsRejected() public {
        bytes32 tooLarge = bytes32(type(uint256).max);

        vm.prank(manager);
        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.NotAFieldElement.selector, tooLarge));
        gate.registerAction(tooLarge, "unprovable");
    }

    function test_anActionCannotBeRegisteredTwiceOrWithoutAName() public {
        vm.startPrank(manager);

        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.ActionAlreadyRegistered.selector, actionId));
        gate.registerAction(actionId, "vault-membership");

        vm.expectRevert(IZkVaultGate.EmptyActionName.selector);
        gate.registerAction(bytes32(uint256(9)), "");

        vm.stopPrank();
    }

    function test_deregisteringAnUnknownActionReverts() public {
        bytes32 other = bytes32(uint256(11));

        vm.prank(manager);
        vm.expectRevert(abi.encodeWithSelector(IZkVaultGate.UnknownAction.selector, other));
        gate.deregisterAction(other);
    }

    function test_registrationRoundTrips() public {
        bytes32 other = bytes32(uint256(12));
        assertFalse(gate.isActionRegistered(other));

        vm.startPrank(manager);
        gate.registerAction(other, "withdraw");
        assertTrue(gate.isActionRegistered(other));

        gate.deregisterAction(other);
        assertFalse(gate.isActionRegistered(other));
        vm.stopPrank();
    }

    // --- access control ---

    function test_onlyTheManagerCanChangeTheRegistry() public {
        bytes32 other = bytes32(uint256(13));

        vm.startPrank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, outsider, Roles.ZK_GATE_MANAGER_ROLE
            )
        );
        gate.registerAction(other, "other");

        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, outsider, Roles.ZK_GATE_MANAGER_ROLE
            )
        );
        gate.deregisterAction(actionId);
        vm.stopPrank();
    }

    function test_upgradeRequiresTheUpgraderRole() public {
        address next = address(new ZkVaultGate());

        vm.prank(outsider);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, outsider, Roles.UPGRADER_ROLE
            )
        );
        gate.upgradeToAndCall(next, "");

        vm.prank(upgrader);
        gate.upgradeToAndCall(next, "");
        assertTrue(gate.isActionRegistered(actionId), "state did not survive the upgrade");
    }

    // --- wiring ---

    function test_theGateReportsItsWiring() public view {
        assertEq(gate.tree(), address(tree));
        assertTrue(gate.verifier() != address(0));
    }

    function test_initializeRejectsZeroAddresses() public {
        address implementation = address(new ZkVaultGate());
        address verifier = _deployVerifier();

        vm.expectRevert(IZkVaultGate.ZeroAddress.selector);
        new ERC1967Proxy(
            implementation, abi.encodeCall(ZkVaultGate.initialize, (address(0), verifier, address(tree)))
        );

        vm.expectRevert(IZkVaultGate.ZeroAddress.selector);
        new ERC1967Proxy(
            implementation, abi.encodeCall(ZkVaultGate.initialize, (admin, address(0), address(tree)))
        );

        vm.expectRevert(IZkVaultGate.ZeroAddress.selector);
        new ERC1967Proxy(
            implementation, abi.encodeCall(ZkVaultGate.initialize, (admin, verifier, address(0)))
        );
    }

    function test_implementationCannotBeInitialized() public {
        ZkVaultGate implementation = new ZkVaultGate();
        address verifier = _deployVerifier();

        vm.expectRevert();
        implementation.initialize(admin, verifier, address(tree));
    }
}
