// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {CommonBase} from "forge-std/Base.sol";
import {StdCheats} from "forge-std/StdCheats.sol";
import {StdUtils} from "forge-std/StdUtils.sol";

import {OracleStaking} from "../../src/oracle/OracleStaking.sol";
import {MockERC20} from "../utils/MockERC20.sol";

contract OracleStakingHandler is CommonBase, StdCheats, StdUtils {
    OracleStaking public immutable staking;
    MockERC20 public immutable token;
    address public immutable slasher;

    address[] public nodes;

    uint256 public ghostStaked;
    uint256 public ghostWithdrawn;
    uint256 public ghostSlashed;

    constructor(
        OracleStaking staking_,
        MockERC20 token_,
        address slasher_
    ) {
        staking = staking_;
        token = token_;
        slasher = slasher_;

        for (uint256 i = 0; i < 4; i++) {
            address node = address(uint160(uint256(keccak256(abi.encode("oracle-node", i)))));
            nodes.push(node);
            token.mint(node, 1_000_000e18);
            vm.prank(node);
            token.approve(address(staking), type(uint256).max);
        }
    }

    function nodeCount() external view returns (uint256) {
        return nodes.length;
    }

    function register(
        uint256 seed,
        uint256 amount
    ) external {
        address node = _node(seed);
        if (staking.isRegistered(node)) return;
        if (staking.activeNodeCount() >= staking.maxNodes()) return;

        amount = bound(amount, staking.minimumStake(), token.balanceOf(node));
        if (amount < staking.minimumStake()) return;

        vm.prank(node);
        staking.register(amount);
        ghostStaked += amount;
    }

    function stake(
        uint256 seed,
        uint256 amount
    ) external {
        address node = _node(seed);
        if (!staking.isRegistered(node)) return;

        uint256 balance = token.balanceOf(node);
        if (balance == 0) return;

        amount = bound(amount, 1, balance);
        vm.prank(node);
        staking.stake(amount);
        ghostStaked += amount;
    }

    function requestUnstake(
        uint256 seed,
        uint256 amount
    ) external {
        address node = _node(seed);
        if (!staking.isRegistered(node)) return;

        uint256 held = staking.stakeOf(node);
        if (held == 0) return;
        if (staking.nodeInfo(node).pendingUnstake != 0) return;

        amount = bound(amount, 1, held);
        vm.prank(node);
        staking.requestUnstake(amount);
    }

    function completeUnstake(
        uint256 seed
    ) external {
        address node = _node(seed);
        OracleStaking.NodeInfo memory info = staking.nodeInfo(node);
        if (info.pendingUnstake == 0) return;
        if (block.timestamp < info.claimableAt) return;

        uint256 before = token.balanceOf(node);
        vm.prank(node);
        staking.completeUnstake();
        ghostWithdrawn += token.balanceOf(node) - before;
    }

    function cancelUnstake(
        uint256 seed
    ) external {
        address node = _node(seed);
        if (staking.nodeInfo(node).pendingUnstake == 0) return;

        vm.prank(node);
        staking.cancelUnstake();
    }

    function slash(
        uint256 seed,
        uint256 amount
    ) external {
        address node = _node(seed);
        if (!staking.isRegistered(node)) return;

        uint256 cap = (staking.stakeOf(node) * staking.maxSlashBps()) / 10_000;
        if (cap == 0) return;

        amount = bound(amount, 1, cap);
        vm.prank(slasher);
        uint256 slashed = staking.slash(node, amount, bytes32("FUZZ"));
        ghostSlashed += slashed;
    }

    function advanceTime(
        uint256 seconds_
    ) external {
        skip(bound(seconds_, 1 hours, 10 days));
    }

    function sumStakes() external view returns (uint256 total) {
        for (uint256 i = 0; i < nodes.length; i++) {
            total += staking.stakeOf(nodes[i]);
        }
    }

    function countActive() external view returns (uint256 total) {
        for (uint256 i = 0; i < nodes.length; i++) {
            if (staking.isActive(nodes[i])) total += 1;
        }
    }

    function anyActiveWithPendingUnstake() external view returns (bool) {
        for (uint256 i = 0; i < nodes.length; i++) {
            OracleStaking.NodeInfo memory info = staking.nodeInfo(nodes[i]);
            if (info.active && info.pendingUnstake != 0) return true;
        }
        return false;
    }

    function _node(
        uint256 seed
    ) private view returns (address) {
        return nodes[seed % nodes.length];
    }
}
