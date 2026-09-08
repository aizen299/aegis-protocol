// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Script} from "forge-std/Script.sol";
import {console2} from "forge-std/console2.sol";

import {Roles} from "../src/shared/access/Roles.sol";
import {VaultEngine} from "../src/vault/VaultEngine.sol";

/// @notice Deploys the v0.1 VaultEngine behind a UUPS proxy and grants the operational roles.
/// @dev DEFAULT_ADMIN_ROLE must be a multisig outside local development.
contract DeployVault is Script {
    function run() external returns (address proxy, address implementation) {
        address admin = vm.envAddress("VAULT_ADMIN");
        address asset = vm.envAddress("VAULT_ASSET");
        address manager = vm.envOr("VAULT_MANAGER", admin);
        address pauser = vm.envOr("VAULT_PAUSER", admin);
        address upgrader = vm.envOr("VAULT_UPGRADER", admin);

        uint256 depositCap = vm.envOr("VAULT_DEPOSIT_CAP", uint256(0));
        uint256 minDeposit = vm.envOr("VAULT_MIN_DEPOSIT", uint256(1e15));
        uint256 maxAllocBps = vm.envOr("VAULT_MAX_ALLOC_BPS", uint256(8_000));

        vm.startBroadcast();

        implementation = address(new VaultEngine());
        bytes memory initData =
            abi.encodeCall(VaultEngine.initialize, (admin, asset, depositCap, minDeposit, maxAllocBps));
        proxy = address(new ERC1967Proxy(implementation, initData));

        VaultEngine vault = VaultEngine(proxy);
        vault.grantRole(Roles.VAULT_MANAGER_ROLE, manager);
        vault.grantRole(Roles.PAUSER_ROLE, pauser);
        vault.grantRole(Roles.UPGRADER_ROLE, upgrader);

        vm.stopBroadcast();

        _writeArtifact(proxy, implementation, asset);

        console2.log("VaultEngine proxy:         ", proxy);
        console2.log("VaultEngine implementation:", implementation);
        console2.log("chainId:                   ", block.chainid);
        console2.log("deployed at block:         ", block.number);
    }

    /// @dev Records the deployment so downstream services read addresses from a file rather than
    ///      from terminal scrollback. Keyed by chain ID: a deployment is only meaningful per chain.
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
