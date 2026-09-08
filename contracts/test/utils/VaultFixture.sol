// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Test} from "forge-std/Test.sol";

import {Roles} from "../../src/shared/access/Roles.sol";
import {VaultEngine} from "../../src/vault/VaultEngine.sol";
import {MockERC20} from "./MockERC20.sol";
import {MockYieldStrategy} from "./MockYieldStrategy.sol";

abstract contract VaultFixture is Test {
    uint256 internal constant DEPOSIT_CAP = 1_000_000e18;
    uint256 internal constant MIN_DEPOSIT = 1e15;
    uint256 internal constant MAX_ALLOC_BPS = 8_000;

    address internal admin = makeAddr("admin");
    address internal manager = makeAddr("manager");
    address internal pauser = makeAddr("pauser");
    address internal upgrader = makeAddr("upgrader");
    address internal alice = makeAddr("alice");
    address internal bob = makeAddr("bob");

    MockERC20 internal token;
    VaultEngine internal vault;
    MockYieldStrategy internal strategy;

    function setUp() public virtual {
        token = new MockERC20("Mock USD", "mUSD", 18);

        VaultEngine implementation = new VaultEngine();
        bytes memory initData = abi.encodeCall(
            VaultEngine.initialize, (admin, address(token), DEPOSIT_CAP, MIN_DEPOSIT, MAX_ALLOC_BPS)
        );
        vault = VaultEngine(address(new ERC1967Proxy(address(implementation), initData)));

        vm.startPrank(admin);
        vault.grantRole(Roles.VAULT_MANAGER_ROLE, manager);
        vault.grantRole(Roles.PAUSER_ROLE, pauser);
        vault.grantRole(Roles.UPGRADER_ROLE, upgrader);
        vm.stopPrank();

        strategy = new MockYieldStrategy(address(token), address(vault));
    }

    function _fund(
        address account,
        uint256 amount
    ) internal {
        token.mint(account, amount);
        vm.prank(account);
        token.approve(address(vault), type(uint256).max);
    }

    function _deposit(
        address account,
        uint256 amount
    ) internal returns (uint256 shares) {
        _fund(account, amount);
        vm.prank(account);
        shares = vault.deposit(amount, account);
    }
}
