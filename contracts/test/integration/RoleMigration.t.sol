// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IAccessControl} from "@openzeppelin/contracts/access/IAccessControl.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";

import {IGovernor} from "../../src/governance/interfaces/IGovernor.sol";
import {ITimelock} from "../../src/governance/interfaces/ITimelock.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {VaultEngine} from "../../src/vault/VaultEngine.sol";
import {GovernorFixture} from "../utils/GovernorFixture.sol";
import {MockERC20} from "../utils/MockERC20.sol";

/// @title The role-migration procedure from docs/v0.3-governance-plan.md §2.4, executed.
/// @dev The procedure is only worth writing down if it has been run. Each test here is one stage of
///      docs/role-migration.md, in the order the runbook performs them.
///
///      Nothing in v0.3 grants governance a live role: this deploys its own vault and migrates that.
contract RoleMigrationTest is GovernorFixture {
    uint256 internal constant DEPOSIT_CAP = 1_000_000e18;
    uint256 internal constant MIN_DEPOSIT = 1e15;
    uint256 internal constant MAX_ALLOC_BPS = 8_000;

    address internal multisig = makeAddr("multisig");

    MockERC20 internal asset;
    VaultEngine internal governedVault;

    function setUp() public override {
        super.setUp();

        asset = new MockERC20("Mock USD", "mUSD", 18);
        governedVault = VaultEngine(
            address(
                new ERC1967Proxy(
                    address(new VaultEngine()),
                    abi.encodeCall(
                        VaultEngine.initialize,
                        (multisig, address(asset), DEPOSIT_CAP, MIN_DEPOSIT, MAX_ALLOC_BPS)
                    )
                )
            )
        );

        vm.prank(multisig);
        governedVault.grantRole(Roles.VAULT_MANAGER_ROLE, multisig);

        _fund(alice, PROPOSAL_THRESHOLD);
        _fund(bob, SUPPLY / 10);
    }

    // --- helpers ---

    /// @dev Drives one proposal through the whole machine: propose, vote, queue, wait, execute.
    function _governanceExecutes(
        address callTarget,
        bytes memory payload
    ) internal returns (uint256 proposalId) {
        IGovernor.Action memory governanceAction = IGovernor.Action({
            targetChainId: block.chainid,
            target: bytes32(uint256(uint160(callTarget))),
            value: 0,
            payload: payload
        });

        vm.prank(alice);
        proposalId = governor.propose(governanceAction, "Migration", "");

        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);
        skip(TIMELOCK_DELAY + 1);
        governor.execute(proposalId);
    }

    function _grantToTimelock(
        bytes32 role
    ) internal {
        vm.prank(multisig);
        governedVault.grantRole(role, address(timelock));
    }

    // --- stage 1: the timelock receives the role while the multisig retains it ---

    /// Both hold it, so a governance system that turns out to be broken has not taken anything
    /// away. This is the only stage that is fully reversible, which is why it comes first.
    function test_stage1_bothHoldTheRole() public {
        _grantToTimelock(Roles.VAULT_MANAGER_ROLE);

        assertTrue(governedVault.hasRole(Roles.VAULT_MANAGER_ROLE, address(timelock)));
        assertTrue(governedVault.hasRole(Roles.VAULT_MANAGER_ROLE, multisig));

        vm.prank(multisig);
        governedVault.setDepositCap(111e18);
        assertEq(governedVault.depositCap(), 111e18, "the multisig lost its own role");
    }

    /// The role goes to the timelock, never to the governor: the timelock is the account that makes
    /// the call. Granting the governor instead would look identical until the first execution.
    function test_stage1_theGovernorItselfHoldsNothing() public {
        _grantToTimelock(Roles.VAULT_MANAGER_ROLE);

        assertFalse(
            governedVault.hasRole(Roles.VAULT_MANAGER_ROLE, address(governor)),
            "the governor was granted a protocol role"
        );

        vm.prank(address(governor));
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector,
                address(governor),
                Roles.VAULT_MANAGER_ROLE
            )
        );
        governedVault.setDepositCap(1);
    }

    // --- stage 2: a proposal exercises the role end to end ---

    function test_stage2_governanceExercisesTheRole() public {
        _grantToTimelock(Roles.VAULT_MANAGER_ROLE);

        uint256 proposalId =
            _governanceExecutes(address(governedVault), abi.encodeCall(VaultEngine.setDepositCap, (777e18)));

        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.EXECUTED));
        assertEq(governedVault.depositCap(), 777e18, "governance could not exercise the role");
    }

    /// The whole point of the delay: a migration proposal is observable before it takes effect.
    function test_stage2_theProposalIsVisibleBeforeItTakesEffect() public {
        _grantToTimelock(Roles.VAULT_MANAGER_ROLE);

        IGovernor.Action memory governanceAction = IGovernor.Action({
            targetChainId: block.chainid,
            target: bytes32(uint256(uint160(address(governedVault)))),
            value: 0,
            payload: abi.encodeCall(VaultEngine.setDepositCap, (777e18))
        });

        vm.prank(alice);
        uint256 proposalId = governor.propose(governanceAction, "Migration", "");
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);

        assertEq(uint8(governor.state(proposalId)), uint8(IGovernor.ProposalState.QUEUED));
        assertEq(governedVault.depositCap(), DEPOSIT_CAP, "the cap moved before the delay elapsed");

        vm.prank(guardian);
        governor.cancel(proposalId);

        skip(TIMELOCK_DELAY + 1);
        assertEq(governedVault.depositCap(), DEPOSIT_CAP, "a cancelled migration still took effect");
    }

    /// Stage 1 exists so this is possible. While the multisig still holds the role it can withdraw
    /// the timelock's, which is the rollback the runbook depends on.
    function test_stage2_theMultisigCanWithdrawTheRoleBeforeRenouncing() public {
        _grantToTimelock(Roles.VAULT_MANAGER_ROLE);

        vm.prank(multisig);
        governedVault.revokeRole(Roles.VAULT_MANAGER_ROLE, address(timelock));

        uint256 proposalId =
            _proposeQueue(address(governedVault), abi.encodeCall(VaultEngine.setDepositCap, (777e18)));

        skip(TIMELOCK_DELAY + 1);

        // Asserted on the specific revert: a bare expectRevert would also pass if execution failed
        // for an unrelated reason, and this test would then prove nothing about the rollback.
        uint256 operationId = governor.proposalOf(proposalId).operationId;
        vm.expectRevert(abi.encodeWithSelector(ITimelock.ExecutionReverted.selector, operationId));
        governor.execute(proposalId);

        assertEq(governedVault.depositCap(), DEPOSIT_CAP, "the rollback did not take effect");
    }

    function _proposeQueue(
        address callTarget,
        bytes memory payload
    ) internal returns (uint256 proposalId) {
        IGovernor.Action memory governanceAction = IGovernor.Action({
            targetChainId: block.chainid,
            target: bytes32(uint256(uint160(callTarget))),
            value: 0,
            payload: payload
        });

        vm.prank(alice);
        proposalId = governor.propose(governanceAction, "Migration", "");
        _passProposal(proposalId, _voters(bob), uint8(IGovernor.Support.FOR));
        governor.queue(proposalId);
    }

    // --- stage 3: the multisig renounces, one role at a time ---

    function test_stage3_governanceStillWorksAfterTheMultisigRenounces() public {
        _grantToTimelock(Roles.VAULT_MANAGER_ROLE);

        // Renounce only after governance has been seen to work, never before.
        _governanceExecutes(address(governedVault), abi.encodeCall(VaultEngine.setDepositCap, (777e18)));

        vm.prank(multisig);
        governedVault.renounceRole(Roles.VAULT_MANAGER_ROLE, multisig);

        assertFalse(governedVault.hasRole(Roles.VAULT_MANAGER_ROLE, multisig));

        vm.prank(multisig);
        vm.expectRevert(
            abi.encodeWithSelector(
                IAccessControl.AccessControlUnauthorizedAccount.selector, multisig, Roles.VAULT_MANAGER_ROLE
            )
        );
        governedVault.setDepositCap(1);

        _governanceExecutes(address(governedVault), abi.encodeCall(VaultEngine.setDepositCap, (888e18)));
        assertEq(governedVault.depositCap(), 888e18, "governance lost the role it was migrated");
    }

    /// Until DEFAULT_ADMIN_ROLE moves, renouncing any other role is reversible: the admin can grant
    /// it back. That is what makes the admin role the last step and the only irreversible one.
    function test_stage3_renouncingIsReversibleWhileTheAdminRoleRemains() public {
        _grantToTimelock(Roles.VAULT_MANAGER_ROLE);

        vm.startPrank(multisig);
        governedVault.renounceRole(Roles.VAULT_MANAGER_ROLE, multisig);
        governedVault.grantRole(Roles.VAULT_MANAGER_ROLE, multisig);
        vm.stopPrank();

        assertTrue(governedVault.hasRole(Roles.VAULT_MANAGER_ROLE, multisig), "renouncing was final");
    }

    // --- stage 4: the admin role, last and irreversible ---

    function test_stage4_governanceCanGrantRolesOnceItHoldsTheAdminRole() public {
        vm.startPrank(multisig);
        governedVault.grantRole(governedVault.DEFAULT_ADMIN_ROLE(), address(timelock));
        governedVault.renounceRole(governedVault.DEFAULT_ADMIN_ROLE(), multisig);
        governedVault.renounceRole(Roles.VAULT_MANAGER_ROLE, multisig);
        vm.stopPrank();

        assertFalse(governedVault.hasRole(governedVault.DEFAULT_ADMIN_ROLE(), multisig));

        // With the admin role held by the timelock, only a passed proposal can hand out a role.
        _governanceExecutes(
            address(governedVault),
            abi.encodeCall(IAccessControl.grantRole, (Roles.VAULT_MANAGER_ROLE, address(timelock)))
        );
        assertTrue(
            governedVault.hasRole(Roles.VAULT_MANAGER_ROLE, address(timelock)),
            "governance cannot administer its own roles"
        );

        _governanceExecutes(address(governedVault), abi.encodeCall(VaultEngine.setDepositCap, (999e18)));
        assertEq(governedVault.depositCap(), 999e18);
    }

    // --- what never moves ---

    /// SLASHER_ROLE is a hot key whose value is being rotatable in minutes. Routing a rotation
    /// through a multi-day timelock is strictly worse for the threat it exists to answer, so the
    /// migration deliberately never reaches it. See docs/v0.3-governance-plan.md §2.4.
    function test_slasherRoleIsNeverMigrated() public {
        _grantToTimelock(Roles.VAULT_MANAGER_ROLE);

        assertFalse(
            governedVault.hasRole(Roles.SLASHER_ROLE, address(timelock)),
            "the slasher role was migrated to a multi-day timelock"
        );
        assertFalse(governedVault.hasRole(Roles.SLASHER_ROLE, address(governor)));
    }

    /// A migration that granted the timelock more than the runbook names would be invisible in a
    /// per-role test. This asserts the negative space.
    function test_theTimelockHoldsOnlyWhatWasMigrated() public {
        _grantToTimelock(Roles.VAULT_MANAGER_ROLE);

        bytes32[5] memory notMigrated = [
            governedVault.DEFAULT_ADMIN_ROLE(),
            Roles.UPGRADER_ROLE,
            Roles.PAUSER_ROLE,
            Roles.SLASHER_ROLE,
            Roles.ORACLE_MANAGER_ROLE
        ];

        for (uint256 i = 0; i < notMigrated.length; i++) {
            assertFalse(
                governedVault.hasRole(notMigrated[i], address(timelock)),
                "the timelock holds a role the runbook did not migrate"
            );
        }
    }
}
