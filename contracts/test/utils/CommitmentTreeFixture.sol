// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Test} from "forge-std/Test.sol";

import {Roles} from "../../src/shared/access/Roles.sol";
import {CommitmentTree} from "../../src/zk/CommitmentTree.sol";
import {IPoseidonT2, IPoseidonT3} from "../../src/zk/poseidon/IPoseidon.sol";
import {PoseidonT2Bytecode, PoseidonT3Bytecode} from "../../src/zk/poseidon/PoseidonBytecode.sol";

abstract contract CommitmentTreeFixture is Test {
    uint32 internal constant ROOT_HISTORY = 8;

    address internal admin = makeAddr("admin");
    address internal writer = makeAddr("writer");
    address internal upgrader = makeAddr("upgrader");
    address internal outsider = makeAddr("outsider");

    IPoseidonT2 internal poseidonT2;
    IPoseidonT3 internal poseidonT3;
    CommitmentTree internal tree;

    /// @dev Mirrors the leaves the contract has accepted, so the test can rebuild the tree from
    ///      scratch and compare against the incremental result.
    bytes32[] internal leaves;

    function setUp() public virtual {
        poseidonT2 = IPoseidonT2(_deploy(PoseidonT2Bytecode.CREATION_CODE));
        poseidonT3 = IPoseidonT3(_deploy(PoseidonT3Bytecode.CREATION_CODE));

        tree = CommitmentTree(
            address(
                new ERC1967Proxy(
                    address(new CommitmentTree()),
                    abi.encodeCall(CommitmentTree.initialize, (admin, address(poseidonT3), ROOT_HISTORY))
                )
            )
        );

        vm.startPrank(admin);
        tree.grantRole(Roles.COMMITMENT_WRITER_ROLE, writer);
        tree.grantRole(Roles.UPGRADER_ROLE, upgrader);
        vm.stopPrank();
    }

    function _commitmentOf(
        uint256 secret
    ) internal view returns (bytes32) {
        return bytes32(poseidonT2.poseidon([secret]));
    }

    function _hash(
        bytes32 left,
        bytes32 right
    ) internal view returns (bytes32) {
        return bytes32(poseidonT3.poseidon([uint256(left), uint256(right)]));
    }

    function _insert(
        bytes32 commitment
    ) internal returns (uint32 index) {
        vm.prank(writer);
        index = tree.insert(commitment);
        leaves.push(commitment);
    }

    /// @dev Rebuilds the whole tree level by level from every leaf, padding with the zero subtree.
    ///      Deliberately naive: the contract's incremental insert is an optimisation, and this is
    ///      the definition it has to match.
    function _naiveRoot() internal view returns (bytes32) {
        uint256 depth = tree.TREE_DEPTH();
        bytes32[] memory level = new bytes32[](leaves.length);
        for (uint256 i = 0; i < leaves.length; i++) {
            level[i] = leaves[i];
        }

        for (uint256 d = 0; d < depth; d++) {
            uint256 width = (level.length + 1) / 2;
            bytes32[] memory next = new bytes32[](width);

            for (uint256 i = 0; i < width; i++) {
                bytes32 left = level[2 * i];
                bytes32 right = 2 * i + 1 < level.length ? level[2 * i + 1] : tree.zeroAt(d);
                next[i] = _hash(left, right);
            }

            level = next;
            if (level.length == 0) return tree.zeroAt(depth);
        }

        return level[0];
    }

    /// @dev The Merkle path for a leaf, in the form the circuit consumes.
    function _pathFor(
        uint256 leafIndex
    ) internal view returns (bytes32[] memory elements, uint256[] memory indices) {
        uint256 depth = tree.TREE_DEPTH();
        elements = new bytes32[](depth);
        indices = new uint256[](depth);

        bytes32[] memory level = new bytes32[](leaves.length);
        for (uint256 i = 0; i < leaves.length; i++) {
            level[i] = leaves[i];
        }

        uint256 index = leafIndex;
        for (uint256 d = 0; d < depth; d++) {
            uint256 siblingIndex = index ^ 1;
            elements[d] = siblingIndex < level.length ? level[siblingIndex] : tree.zeroAt(d);
            indices[d] = index % 2;

            uint256 width = (level.length + 1) / 2;
            bytes32[] memory next = new bytes32[](width);
            for (uint256 i = 0; i < width; i++) {
                bytes32 left = level[2 * i];
                bytes32 right = 2 * i + 1 < level.length ? level[2 * i + 1] : tree.zeroAt(d);
                next[i] = _hash(left, right);
            }

            level = next;
            index /= 2;
        }
    }

    /// @dev The circuit's compute_root, in Solidity. If the two disagree, a proof that verifies in
    ///      the circuit will not match any root the contract holds.
    function _rootFromPath(
        bytes32 leaf,
        bytes32[] memory elements,
        uint256[] memory indices
    ) internal view returns (bytes32) {
        bytes32 current = leaf;
        for (uint256 d = 0; d < elements.length; d++) {
            current = indices[d] == 0 ? _hash(current, elements[d]) : _hash(elements[d], current);
        }
        return current;
    }

    function _deploy(
        bytes memory creationCode
    ) internal returns (address deployed) {
        assembly ("memory-safe") {
            deployed := create(0, add(creationCode, 0x20), mload(creationCode))
        }
        require(deployed != address(0), "poseidon deployment failed");
    }
}
