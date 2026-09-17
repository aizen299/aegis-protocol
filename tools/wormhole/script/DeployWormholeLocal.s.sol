// SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.28;

import "forge-std/Script.sol";

import "../src/wormhole/Implementation.sol";
import "../src/wormhole/Setup.sol";
import "../src/wormhole/Wormhole.sol";

/// @notice Deploys Wormhole's core to a local chain with a single guardian. Local only.
contract DeployWormholeLocal is Script {
    /// @dev Wormhole's chain id for Arbitrum, and Solana's, which governs the core itself.
    uint16 internal constant ARBITRUM_WORMHOLE_CHAIN = 23;
    uint16 internal constant GOVERNANCE_CHAIN = 1;

    function run() external returns (address core) {
        uint256 deployerKey = vm.envUint("PRIVATE_KEY");
        address[] memory guardians = new address[](1);
        guardians[0] = vm.envAddress("WORMHOLE_GUARDIAN");

        vm.startBroadcast(deployerKey);
        Implementation implementation = new Implementation();
        Setup setup = new Setup();
        core = address(
            new Wormhole(
                address(setup),
                abi.encodeWithSelector(
                    Setup.setup.selector,
                    address(implementation),
                    guardians,
                    ARBITRUM_WORMHOLE_CHAIN,
                    GOVERNANCE_CHAIN,
                    bytes32(uint256(4)),
                    block.chainid
                )
            )
        );
        vm.stopBroadcast();

        string memory out = vm.serializeAddress("wormhole", "core", core);
        vm.writeJson(out, vm.envString("WORMHOLE_ARTIFACT"));
        console.log("WormholeCore:", core);
    }
}
