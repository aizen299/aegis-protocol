// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Script} from "forge-std/Script.sol";
import {console2} from "forge-std/console2.sol";

import {HonkVerifier} from "../generated/HonkVerifier.sol";
import {Roles} from "../src/shared/access/Roles.sol";
import {CommitmentTree} from "../src/zk/CommitmentTree.sol";
import {ZkVaultGate} from "../src/zk/ZkVaultGate.sol";
import {PoseidonT2Bytecode, PoseidonT3Bytecode} from "../src/zk/poseidon/PoseidonBytecode.sol";

/// @notice Local-only zk deployment for development and the end-to-end test.
/// @dev Never run this against a public network: it grants every role to one key.
contract DeployZkLocal is Script {
    uint32 internal constant ROOT_HISTORY = 30;

    function run()
        external
        returns (
            address poseidonT2,
            address poseidonT3,
            address treeProxy,
            address verifier,
            address gateProxy
        )
    {
        uint256 deployerKey = vm.envUint("PRIVATE_KEY");
        address deployer = vm.addr(deployerKey);

        vm.startBroadcast(deployerKey);

        poseidonT2 = _deploy(PoseidonT2Bytecode.CREATION_CODE);
        poseidonT3 = _deploy(PoseidonT3Bytecode.CREATION_CODE);

        treeProxy = address(
            new ERC1967Proxy(
                address(new CommitmentTree()),
                abi.encodeCall(CommitmentTree.initialize, (deployer, poseidonT3, ROOT_HISTORY))
            )
        );

        verifier = address(new HonkVerifier());

        gateProxy = address(
            new ERC1967Proxy(
                address(new ZkVaultGate()),
                abi.encodeCall(ZkVaultGate.initialize, (deployer, verifier, treeProxy))
            )
        );

        CommitmentTree(treeProxy).grantRole(Roles.COMMITMENT_WRITER_ROLE, deployer);
        ZkVaultGate(gateProxy).grantRole(Roles.ZK_GATE_MANAGER_ROLE, deployer);

        vm.stopBroadcast();

        _writeArtifact(poseidonT2, poseidonT3, treeProxy, verifier, gateProxy);

        console2.log("PoseidonT2:     ", poseidonT2);
        console2.log("PoseidonT3:     ", poseidonT3);
        console2.log("CommitmentTree: ", treeProxy);
        console2.log("HonkVerifier:   ", verifier);
        console2.log("ZkVaultGate:    ", gateProxy);
    }

    function _deploy(
        bytes memory creationCode
    ) internal returns (address deployed) {
        assembly ("memory-safe") {
            deployed := create(0, add(creationCode, 0x20), mload(creationCode))
        }
        require(deployed != address(0), "poseidon deployment failed");
    }

    function _writeArtifact(
        address poseidonT2,
        address poseidonT3,
        address treeProxy,
        address verifier,
        address gateProxy
    ) internal {
        string memory key = "zk";
        vm.serializeAddress(key, "poseidonT2", poseidonT2);
        vm.serializeAddress(key, "poseidonT3", poseidonT3);
        vm.serializeAddress(key, "commitmentTree", treeProxy);
        vm.serializeAddress(key, "verifier", verifier);
        vm.serializeAddress(key, "zkVaultGate", gateProxy);
        vm.serializeUint(key, "chainId", block.chainid);
        string memory out = vm.serializeUint(key, "deployedAtBlock", block.number);

        vm.writeJson(out, string.concat("./deployments/zk-", vm.toString(block.chainid), ".json"));
    }
}
