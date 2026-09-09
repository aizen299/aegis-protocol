// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";

import {HonkVerifier} from "../../generated/HonkVerifier.sol";
import {IGovernor} from "../../src/governance/interfaces/IGovernor.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {ZkVaultGate} from "../../src/zk/ZkVaultGate.sol";
import {CommitmentTreeFixture} from "../utils/CommitmentTreeFixture.sol";
import {GovernorFixture} from "../utils/GovernorFixture.sol";
import {OracleRoundsFixture} from "../utils/OracleRoundsFixture.sol";
import {ProofFixture} from "../utils/ProofFixture.sol";
import {GovernanceHandler} from "./GovernanceHandler.sol";
import {OracleRoundsHandler} from "./OracleRoundsHandler.sol";
import {ZkHandler} from "./ZkHandler.sol";

/// @dev Not an invariant suite: drives the handler deterministically so a starved fuzz run can be
///      diagnosed instead of guessed at.
contract HandlerProbeTest is OracleRoundsFixture {
    OracleRoundsHandler internal handler;

    function setUp() public override {
        super.setUp();
        _spawnNodes(5);

        address[] memory addrs = new address[](keyed.length);
        uint256[] memory keys = new uint256[](keyed.length);
        for (uint256 i = 0; i < keyed.length; i++) {
            addrs[i] = keyed[i].addr;
            keys[i] = keyed[i].key;
        }

        handler = new OracleRoundsHandler(rounds, staking, admin, FEED, addrs, keys);
        vm.prank(admin);
        staking.grantRole(0x00, address(handler));
    }

    function test_handlerCanDeactivateAndReactivate() public {
        handler.deactivateNode(0);
        assertEq(staking.activeNodeCount(), 4, "deactivate did not take effect");

        handler.reactivateNode(0);
        assertEq(staking.activeNodeCount(), 5, "reactivate did not take effect");
    }

    function test_handlerCanOpenSubmitAndSettle() public {
        handler.openRound(0);
        assertEq(handler.ghostOpenedCount(), 1, "openRound was blocked");

        for (uint256 i = 0; i < 5; i++) {
            handler.submit(i, 3000e18);
        }
        assertEq(handler.ghostSubmittedCount(), 5, "submit was blocked");

        handler.settle(0);
        assertEq(handler.ghostSettledCount(), 1, "settle was blocked");
    }
}

/// @dev Same purpose for the governance suite. Every §6 invariant is trivially true if no proposal
///      ever executes, and each handler action guards itself with an early return, so a starved run
///      would look identical to a passing one. This drives the whole lifecycle deterministically.
contract GovernanceProbeTest is GovernorFixture {
    GovernanceHandler internal handler;

    function setUp() public override {
        super.setUp();

        address[] memory actors = new address[](3);
        actors[0] = alice;
        actors[1] = bob;
        actors[2] = carol;
        for (uint256 i = 0; i < actors.length; i++) {
            _fund(actors[i], SUPPLY / 5);
        }

        handler = new GovernanceHandler(token, governor, timelock, target, guardian, actors);
    }

    function test_handlerCanDriveAProposalToExecution() public {
        handler.propose(0, 42, false);
        assertEq(handler.ghostProposedCount(), 1, "propose was blocked");

        uint256 proposalId = handler.proposalAt(0);

        handler.advanceTime(3 days);
        assertEq(
            uint8(governor.state(proposalId)),
            uint8(IGovernor.ProposalState.ACTIVE),
            "the proposal never opened for voting"
        );

        handler.castVote(0, 0, uint8(IGovernor.Support.FOR));
        handler.castVote(0, 1, uint8(IGovernor.Support.FOR));
        assertEq(handler.ghostVoteCount(), 2, "castVote was blocked");

        for (uint256 i = 0; i < 3; i++) {
            handler.advanceTime(3 days);
        }

        handler.queue(0);
        assertEq(handler.ghostQueuedCount(), 1, "queue was blocked");

        handler.advanceTime(3 days);
        handler.execute(0);
        assertEq(handler.ghostExecutedCount(), 1, "execute was blocked");
        assertEq(target.value(), 42, "the action never ran");
    }

    function test_handlerCanCancelAndCanMoveWeight() public {
        handler.propose(1, 7, false);
        handler.cancel(0, true);
        assertEq(handler.ghostCancelledCount(), 1, "cancel was blocked");

        uint256 before = token.balanceOf(carol);
        handler.moveWeight(0, 2, 1 ether);
        assertEq(token.balanceOf(carol), before + 1 ether, "moveWeight was blocked");
    }

    /// The remote branch has to be reachable, or invariant_remoteProposalNeverPerformsALocalCall
    /// is proving something about an input the fuzzer never produces.
    function test_handlerCanDriveARemoteProposalToARefusedExecution() public {
        handler.propose(0, 99, true);
        uint256 proposalId = handler.proposalAt(0);
        assertTrue(handler.isRemote(proposalId), "the remote branch was not taken");

        handler.advanceTime(3 days);
        handler.castVote(0, 0, uint8(IGovernor.Support.FOR));
        handler.castVote(0, 1, uint8(IGovernor.Support.FOR));
        for (uint256 i = 0; i < 3; i++) {
            handler.advanceTime(3 days);
        }
        handler.queue(0);
        handler.advanceTime(3 days);

        handler.execute(0);
        assertEq(handler.ghostExecutedCount(), 0, "a remote proposal executed");
        assertEq(target.value(), 0, "a remote action landed locally");
        assertFalse(handler.ghostRemoteCallLandedLocally());
    }
}

/// @dev Same purpose for the zk suite. Every §6 invariant here is trivially true if the handler
///      never executes a proof, and each action guards itself with an early return, so a starved
///      run would look identical to a passing one.
contract ZkProbeTest is CommitmentTreeFixture {
    address internal manager = makeAddr("manager");

    ZkVaultGate internal gate;
    ZkHandler internal handler;

    function setUp() public override {
        super.setUp();

        gate = _deployGateAtAFixedAddress(_deployVerifier(), admin, address(tree));

        vm.prank(admin);
        gate.grantRole(Roles.ZK_GATE_MANAGER_ROLE, manager);
        vm.prank(manager);
        gate.registerAction(ProofFixture.ACTION_ID, "vault-membership");

        handler = new ZkHandler(
            tree,
            gate,
            writer,
            manager,
            ProofFixture.PROOF,
            ProofFixture.ROOT,
            ProofFixture.NULLIFIER_HASH,
            ProofFixture.ACTION_ID
        );

        vm.prank(admin);
        tree.grantRole(Roles.COMMITMENT_WRITER_ROLE, address(handler));

        // The commitment the fixture's proof was built for. Without it the tree never holds the
        // root the proof binds, every execution is refused as an unknown root, and the replay
        // invariants below pass while proving nothing.
        _insert(_commitmentOf(424242));
    }

    function test_handlerCanInsertAndRefuseADuplicate() public {
        handler.insert(1);
        assertEq(handler.ghostInsertCount(), 1, "insert was blocked");

        handler.insertDuplicate(0);
        assertEq(handler.ghostRefusedCount(), 1, "the duplicate was not refused");
        assertEq(handler.insertedCount(), 1, "a duplicate was recorded as inserted");
    }

    /// The one that matters: without a valid proof reaching execution, every replay invariant is
    /// proving something about a code path the fuzzer never enters.
    function test_handlerCanExecuteTheValidProofExactlyOnce() public {
        handler.executeValidProof();
        assertEq(handler.ghostExecuteCount(), 1, "the valid proof never executed");
        assertTrue(gate.isSpent(ProofFixture.NULLIFIER_HASH));

        handler.executeValidProof();
        assertEq(handler.ghostExecuteCount(), 1, "the proof executed twice");
        assertFalse(handler.ghostDoubleSpend(), "a double spend was recorded as success");
    }

    function test_handlerCanReachTheRejectionPaths() public {
        uint256 refusedBefore = handler.ghostRefusedCount();

        handler.executeAgainstAnInventedRoot(7);
        handler.executeAgainstAnUnregisteredAction(9);

        assertEq(handler.ghostRefusedCount(), refusedBefore + 2, "a rejection path was not reached");
        assertFalse(handler.ghostUnknownRootAccepted());
        assertFalse(handler.ghostUnregisteredActionAccepted());
    }

    function test_handlerCanChurnTheRegistry() public {
        handler.executeValidProof();
        assertTrue(gate.isSpent(ProofFixture.NULLIFIER_HASH));

        handler.churnActionRegistry(0);
        assertFalse(gate.isActionRegistered(ProofFixture.ACTION_ID), "deregister was blocked");

        handler.churnActionRegistry(0);
        assertTrue(gate.isActionRegistered(ProofFixture.ACTION_ID), "register was blocked");
        assertFalse(handler.ghostSpentBecameUnspent(), "churn un-spent a nullifier");
    }
}
