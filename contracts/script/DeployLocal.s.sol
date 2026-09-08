// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Script} from "forge-std/Script.sol";
import {console2} from "forge-std/console2.sol";

import {Roles} from "../src/shared/access/Roles.sol";
import {VaultEngine} from "../src/vault/VaultEngine.sol";
import {MockERC20} from "../test/utils/MockERC20.sol";

/// @notice Local-only deployment: a test token plus a vault, wired and funded for development and
///         the end-to-end smoke test. Never run this against a public network.
/// @dev The token has six decimals deliberately. Eighteen is the value every layer would get right
///      by accident, so the development default is the one that catches a wrong assumption.
contract DeployLocal is Script {
    uint8 internal constant ASSET_DECIMALS = 6;
    uint256 internal constant MINT_AMOUNT = 1_000_000e6;

    function run() external returns (address proxy, address token) {
        uint256 deployerKey = vm.envUint("PRIVATE_KEY");
        address deployer = vm.addr(deployerKey);

        vm.startBroadcast(deployerKey);

        MockERC20 asset = new MockERC20("Test USD", "tUSD", ASSET_DECIMALS);
        asset.mint(deployer, MINT_AMOUNT);

        address implementation = address(new VaultEngine());
        bytes memory initData =
            abi.encodeCall(VaultEngine.initialize, (deployer, address(asset), 0, 1e6, 8_000));
        proxy = address(new ERC1967Proxy(implementation, initData));

        VaultEngine vault = VaultEngine(proxy);
        vault.grantRole(Roles.VAULT_MANAGER_ROLE, deployer);
        vault.grantRole(Roles.PAUSER_ROLE, deployer);
        vault.grantRole(Roles.UPGRADER_ROLE, deployer);

        vm.stopBroadcast();

        token = address(asset);
        _writeArtifact(proxy, implementation, token);

        console2.log("asset (6 decimals):", token);
        console2.log("VaultEngine proxy: ", proxy);
        console2.log("deployer:          ", deployer);
    }

    function _writeArtifact(
        address proxy,
        address implementation,
        address asset
    ) internal {
        string memory key = "deployment";
        vm.serializeAddress(key, "vaultEngineProxy", proxy);
        vm.serializeAddress(key, "vaultEngineImplementation", implementation);
        vm.serializeAddress(key, "asset", asset);
        vm.serializeUint(key, "chainId", block.chainid);
        string memory out = vm.serializeUint(key, "deployedAtBlock", block.number);

        vm.writeJson(out, string.concat("./deployments/", vm.toString(block.chainid), ".json"));
    }
}
