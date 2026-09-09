// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Test} from "forge-std/Test.sol";

import {AegisToken} from "../../src/governance/AegisToken.sol";
import {Governor} from "../../src/governance/Governor.sol";
import {Timelock} from "../../src/governance/Timelock.sol";
import {IGovernor} from "../../src/governance/interfaces/IGovernor.sol";
import {ITimelock} from "../../src/governance/interfaces/ITimelock.sol";
import {Roles} from "../../src/shared/access/Roles.sol";

/// @dev A contract a proposal can act on, so execution is observable rather than inferred.
contract GovernedTarget {
    uint256 public value;
    uint256 public received;

    error Refused();

    function setValue(
        uint256 newValue
    ) external {
        value = newValue;
    }

    function refuse() external pure {
        revert Refused();
    }

    receive() external payable {
        received += msg.value;
    }
}

abstract contract GovernorFixture is Test {
    uint256 internal constant SUPPLY = 100_000_000e18;
    uint48 internal constant VOTING_DELAY = 1 days;
    uint48 internal constant VOTING_PERIOD = 7 days;
    uint48 internal constant TIMELOCK_DELAY = 2 days;
    uint256 internal constant PROPOSAL_THRESHOLD = SUPPLY / 100; // 1%
    uint256 internal constant QUORUM_NUMERATOR = 4; // 4%

    address internal admin = makeAddr("admin");
    address internal guardian = makeAddr("guardian");
    address internal upgrader = makeAddr("upgrader");
    address internal treasury = makeAddr("treasury");
    address internal alice = makeAddr("alice");
    address internal bob = makeAddr("bob");
    address internal carol = makeAddr("carol");

    AegisToken internal token;
    Governor internal governor;
    Timelock internal timelock;
    GovernedTarget internal target;

    function setUp() public virtual {
        token = new AegisToken(treasury, SUPPLY);
        target = new GovernedTarget();

        timelock = Timelock(
            payable(address(
                    new ERC1967Proxy(
                        address(new Timelock()), abi.encodeCall(Timelock.initialize, (admin, TIMELOCK_DELAY))
                    )
                ))
        );

        bytes memory initData = abi.encodeCall(
            Governor.initialize,
            (
                admin,
                address(token),
                address(timelock),
                VOTING_DELAY,
                VOTING_PERIOD,
                PROPOSAL_THRESHOLD,
                QUORUM_NUMERATOR
            )
        );
        governor = Governor(address(new ERC1967Proxy(address(new Governor()), initData)));

        vm.startPrank(admin);
        governor.grantRole(Roles.GOVERNANCE_GUARDIAN_ROLE, guardian);
        governor.grantRole(Roles.UPGRADER_ROLE, upgrader);
        timelock.grantRole(Roles.UPGRADER_ROLE, upgrader);
        // The governor is the timelock's only client: nothing else may schedule, execute, or
        // cancel, or a proposal's state machine and the queue could disagree.
        timelock.grantRole(Roles.TIMELOCK_PROPOSER_ROLE, address(governor));
        timelock.grantRole(Roles.TIMELOCK_EXECUTOR_ROLE, address(governor));
        timelock.grantRole(Roles.TIMELOCK_CANCELLER_ROLE, address(governor));
        vm.stopPrank();

        // Start well past zero so snapshots can look backwards.
        skip(30 days);
    }

    /// @dev Gives `holder` voting power and self-delegates, which is the only way a balance votes.
    function _fund(
        address holder,
        uint256 amount
    ) internal {
        vm.prank(treasury);
        token.transfer(holder, amount);

        vm.prank(holder);
        token.delegate(holder);
    }

    function _localAction(
        uint256 newValue
    ) internal view returns (IGovernor.Action memory) {
        return IGovernor.Action({
            targetChainId: block.chainid,
            target: bytes32(uint256(uint160(address(target)))),
            value: 0,
            payload: abi.encodeCall(GovernedTarget.setValue, (newValue))
        });
    }

    function _propose(
        address proposer,
        uint256 newValue
    ) internal returns (uint256) {
        vm.prank(proposer);
        return governor.propose(_localAction(newValue), "Set value", "because");
    }

    /// @dev Advances to the voting window, votes, then advances past its end.
    function _passProposal(
        uint256 proposalId,
        address[] memory voters,
        uint8 support
    ) internal {
        skip(VOTING_DELAY + 1);
        for (uint256 i = 0; i < voters.length; i++) {
            vm.prank(voters[i]);
            governor.castVote(proposalId, support, "");
        }
        skip(VOTING_PERIOD);
    }

    function _voters(
        address a
    ) internal pure returns (address[] memory out) {
        out = new address[](1);
        out[0] = a;
    }

    function _voters(
        address a,
        address b
    ) internal pure returns (address[] memory out) {
        out = new address[](2);
        out[0] = a;
        out[1] = b;
    }
}
