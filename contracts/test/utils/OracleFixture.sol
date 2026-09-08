// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Test} from "forge-std/Test.sol";

import {OracleStaking} from "../../src/oracle/OracleStaking.sol";
import {Roles} from "../../src/shared/access/Roles.sol";
import {MockERC20} from "./MockERC20.sol";

abstract contract OracleFixture is Test {
    uint256 internal constant MIN_STAKE = 10_000e18;
    uint256 internal constant STAKE_FLOOR = 5_000e18;
    uint256 internal constant UNBONDING = 7 days;
    uint256 internal constant MAX_SLASH_BPS = 1_000;
    uint256 internal constant MAX_NODES = 31;

    bytes32 internal constant REASON_OUTLIER = bytes32("OUTLIER_SUBMISSION");
    bytes32 internal constant REASON_MISSED = bytes32("MISSED_ROUND");

    address internal admin = makeAddr("admin");
    address internal oracleManager = makeAddr("oracleManager");
    address internal slasher = makeAddr("slasher");
    address internal upgrader = makeAddr("upgrader");
    address internal nodeA = makeAddr("nodeA");
    address internal nodeB = makeAddr("nodeB");
    address internal outsider = makeAddr("outsider");

    MockERC20 internal stakeToken;
    OracleStaking internal staking;

    function setUp() public virtual {
        stakeToken = new MockERC20("Stake", "STK", 18);

        OracleStaking implementation = new OracleStaking();
        bytes memory initData = abi.encodeCall(
            OracleStaking.initialize,
            (admin, address(stakeToken), MIN_STAKE, STAKE_FLOOR, UNBONDING, MAX_SLASH_BPS, MAX_NODES)
        );
        staking = OracleStaking(address(new ERC1967Proxy(address(implementation), initData)));

        vm.startPrank(admin);
        staking.grantRole(Roles.ORACLE_MANAGER_ROLE, oracleManager);
        staking.grantRole(Roles.SLASHER_ROLE, slasher);
        staking.grantRole(Roles.UPGRADER_ROLE, upgrader);
        vm.stopPrank();
    }

    function _fundNode(
        address node,
        uint256 amount
    ) internal {
        stakeToken.mint(node, amount);
        vm.prank(node);
        stakeToken.approve(address(staking), type(uint256).max);
    }

    function _registerNode(
        address node,
        uint256 amount
    ) internal {
        _fundNode(node, amount);
        vm.prank(node);
        staking.register(amount);
    }
}
