// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {CommonBase} from "forge-std/Base.sol";
import {StdCheats} from "forge-std/StdCheats.sol";
import {StdUtils} from "forge-std/StdUtils.sol";

import {VaultEngine} from "../../src/vault/VaultEngine.sol";
import {MockERC20} from "../utils/MockERC20.sol";
import {MockYieldStrategy} from "../utils/MockYieldStrategy.sol";

/// @dev Drives the vault with bounded random actions. Ghost variables track what the invariants
///      cannot read directly from the contract.
contract VaultHandler is CommonBase, StdCheats, StdUtils {
    VaultEngine public immutable vault;
    MockERC20 public immutable token;
    MockYieldStrategy public immutable strategy;
    address public immutable manager;

    address[] public actors;

    uint256 public ghostDeposited;
    uint256 public ghostWithdrawn;
    uint256 public ghostYield;
    bool public ghostCapBreachedAtAllocation;

    constructor(
        VaultEngine vault_,
        MockERC20 token_,
        MockYieldStrategy strategy_,
        address manager_
    ) {
        vault = vault_;
        token = token_;
        strategy = strategy_;
        manager = manager_;

        for (uint256 i = 0; i < 3; i++) {
            address actor = address(uint160(uint256(keccak256(abi.encode("actor", i)))));
            actors.push(actor);
            token.mint(actor, 1_000_000e18);
            vm.prank(actor);
            token.approve(address(vault), type(uint256).max);
        }
    }

    function actorCount() external view returns (uint256) {
        return actors.length;
    }

    function deposit(
        uint256 actorSeed,
        uint256 amount
    ) external {
        address actor = _actor(actorSeed);
        uint256 headroom = vault.depositCap() - _min(vault.totalAssets(), vault.depositCap());
        if (headroom < vault.minDeposit()) return;

        amount = bound(amount, vault.minDeposit(), _min(headroom, token.balanceOf(actor)));
        if (amount < vault.minDeposit()) return;

        vm.prank(actor);
        vault.deposit(amount, actor);
        ghostDeposited += amount;
    }

    function withdraw(
        uint256 actorSeed,
        uint256 shareSeed
    ) external {
        address actor = _actor(actorSeed);
        uint256 held = vault.sharesOf(actor);
        if (held == 0) return;

        uint256 shares = bound(shareSeed, 1, held);
        if (vault.convertToAssets(shares) == 0) return;

        vm.prank(actor);
        uint256 assets = vault.withdraw(shares, actor);
        ghostWithdrawn += assets;
    }

    function allocate(
        uint256 amount
    ) external {
        if (vault.strategy() == address(0)) return;

        uint256 cap = (vault.totalAssets() * vault.maxStrategyAllocationBps()) / 10_000;
        uint256 allocated = vault.allocatedAssets();
        if (allocated >= cap) return;

        uint256 room = _min(cap - allocated, vault.idleAssets());
        if (room == 0) return;

        amount = bound(amount, 1, room);
        vm.prank(manager);
        vault.allocate(amount);

        uint256 capAfter = (vault.totalAssets() * vault.maxStrategyAllocationBps()) / 10_000;
        if (vault.allocatedAssets() > capAfter) ghostCapBreachedAtAllocation = true;
    }

    function deallocate(
        uint256 amount
    ) external {
        uint256 allocated = vault.allocatedAssets();
        if (allocated == 0) return;

        amount = bound(amount, 1, allocated);
        vm.prank(manager);
        vault.deallocate(amount);
    }

    function accrueYield(
        uint256 amount
    ) external {
        if (vault.totalShares() == 0) return;

        amount = bound(amount, 0, 1_000e18);
        if (amount == 0) return;

        token.mint(address(vault), amount);
        ghostYield += amount;
    }

    function sumActorShares() external view returns (uint256 total) {
        for (uint256 i = 0; i < actors.length; i++) {
            total += vault.sharesOf(actors[i]);
        }
    }

    function _actor(
        uint256 seed
    ) private view returns (address) {
        return actors[seed % actors.length];
    }

    function _min(
        uint256 a,
        uint256 b
    ) private pure returns (uint256) {
        return a < b ? a : b;
    }
}
