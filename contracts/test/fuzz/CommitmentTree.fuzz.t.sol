// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ICommitmentTree} from "../../src/zk/interfaces/ICommitmentTree.sol";
import {CommitmentTreeFixture} from "../utils/CommitmentTreeFixture.sol";

contract CommitmentTreeFuzzTest is CommitmentTreeFixture {
    uint256 internal constant FIELD_SIZE =
        21888242871839275222246405745257275088548364400416034343698204186575808495617;

    /// Whatever the leaves are, the incremental root equals a full rebuild. The unit test checks a
    /// fixed sequence; this checks the shape of the sequence does not matter.
    function testFuzz_theIncrementalRootMatchesARebuild(
        uint256 seed,
        uint8 count
    ) public {
        uint256 n = bound(count, 1, 12);

        for (uint256 i = 0; i < n; i++) {
            bytes32 commitment = bytes32(uint256(keccak256(abi.encode(seed, i))) % FIELD_SIZE);
            if (tree.isCommitmentInserted(commitment)) continue;

            _insert(commitment);
            assertEq(tree.currentRoot(), _naiveRoot(), "roots diverged");
        }
    }

    /// A path pulled from the tree reaches the root the way the circuit walks it, for any leaf in
    /// any tree the fuzzer builds.
    function testFuzz_everyLeafHasAPathToTheRoot(
        uint256 seed,
        uint8 count
    ) public {
        uint256 n = bound(count, 1, 8);

        for (uint256 i = 0; i < n; i++) {
            bytes32 commitment = bytes32(uint256(keccak256(abi.encode(seed, i))) % FIELD_SIZE);
            if (tree.isCommitmentInserted(commitment)) continue;
            _insert(commitment);
        }

        for (uint256 leafIndex = 0; leafIndex < leaves.length; leafIndex++) {
            (bytes32[] memory elements, uint256[] memory indices) = _pathFor(leafIndex);
            assertEq(
                _rootFromPath(leaves[leafIndex], elements, indices),
                tree.currentRoot(),
                "a leaf has no path to the root"
            );
        }
    }

    /// The field bound is exact in both directions: one below is accepted, the modulus itself is
    /// not. An off-by-one here means the circuit and the contract read the same bytes differently.
    function testFuzz_theFieldBoundaryIsExact(
        uint256 raw
    ) public {
        bytes32 commitment = bytes32(raw);

        if (raw < FIELD_SIZE) {
            vm.prank(writer);
            tree.insert(commitment);
            assertTrue(tree.isCommitmentInserted(commitment));
            return;
        }

        vm.prank(writer);
        vm.expectRevert(abi.encodeWithSelector(ICommitmentTree.CommitmentNotInField.selector, commitment));
        tree.insert(commitment);
    }

    /// Every insert produces a root the tree will still accept, and a root it never produced is
    /// never accepted — the property the gate's membership check rests on.
    function testFuzz_onlyRootsTheTreeProducedAreKnown(
        uint256 seed,
        uint8 count
    ) public {
        uint256 n = bound(count, 1, 5);

        for (uint256 i = 0; i < n; i++) {
            bytes32 commitment = bytes32(uint256(keccak256(abi.encode(seed, i))) % FIELD_SIZE);
            if (tree.isCommitmentInserted(commitment)) continue;

            _insert(commitment);
            assertTrue(tree.isKnownRoot(tree.currentRoot()), "a produced root is unknown");
        }

        bytes32 invented = bytes32(uint256(keccak256(abi.encode("invented", seed))) % FIELD_SIZE);
        assertFalse(tree.isKnownRoot(invented), "an invented root was accepted");
    }

    function testFuzz_aDuplicateIsAlwaysRejected(
        uint256 raw
    ) public {
        bytes32 commitment = bytes32(bound(raw, 1, FIELD_SIZE - 1));

        vm.prank(writer);
        tree.insert(commitment);

        vm.prank(writer);
        vm.expectRevert(abi.encodeWithSelector(ICommitmentTree.DuplicateCommitment.selector, commitment));
        tree.insert(commitment);
    }
}
