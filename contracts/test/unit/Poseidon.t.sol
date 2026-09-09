// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {Test} from "forge-std/Test.sol";

import {IPoseidonT2, IPoseidonT3} from "../../src/zk/poseidon/IPoseidon.sol";
import {PoseidonT2Bytecode, PoseidonT3Bytecode} from "../../src/zk/poseidon/PoseidonBytecode.sol";

/// @title The contract and the circuit must compute the same hash.
/// @dev This is the seam docs/v0.4-zk-plan.md §2.8 exists to close. The circuit uses Noir's
///      circomlib-compatible Poseidon and the contract uses circomlib's generated one; the claim
///      that they agree is worth nothing unless something checks it.
///
///      The vectors below are asserted identically in zk/circuits/vault_membership/src/main.nr.
///      If either side's hash family changes, one of the two suites fails.
contract PoseidonTest is Test {
    /// circomlib Poseidon([1]) — the same constant zk's `the_hash_family_is_circomlib_compatible`
    /// asserts.
    uint256 internal constant HASH_1_OF_1 =
        0x29176100eaa962bdc1fe6c654d6a3c130e96a4d1168b33848b897dc502820133;

    /// circomlib Poseidon([1, 2]).
    uint256 internal constant HASH_2_OF_1_2 =
        0x115cc0f5e7d690413df64c6b9662e9cf2a3617f2743245519e19607a4417189a;

    IPoseidonT2 internal t2;
    IPoseidonT3 internal t3;

    function setUp() public {
        t2 = IPoseidonT2(_deploy(PoseidonT2Bytecode.CREATION_CODE));
        t3 = IPoseidonT3(_deploy(PoseidonT3Bytecode.CREATION_CODE));
    }

    function test_theGeneratedPoseidonMatchesTheCircuitsVectors() public view {
        assertEq(t2.poseidon([uint256(1)]), HASH_1_OF_1, "T2 disagrees with the circuit");
        assertEq(t3.poseidon([uint256(1), uint256(2)]), HASH_2_OF_1_2, "T3 disagrees with the circuit");
    }

    /// Order matters in a Merkle path, so a hash that ignored it would make the path index
    /// meaningless on chain while the circuit still constrained it.
    function test_theHashIsOrderSensitive() public view {
        assertTrue(
            t3.poseidon([uint256(1), uint256(2)]) != t3.poseidon([uint256(2), uint256(1)]),
            "the generated Poseidon ignores argument order"
        );
    }

    /// A tree of any depth is repeated application of the same hash, so determinism across calls
    /// is what makes a recomputed root reproducible.
    function testFuzz_theHashIsDeterministic(
        uint128 left,
        uint128 right
    ) public view {
        uint256 first = t3.poseidon([uint256(left), uint256(right)]);
        uint256 second = t3.poseidon([uint256(left), uint256(right)]);
        assertEq(first, second);
    }

    function test_theGeneratedCodeDeploys() public view {
        assertGt(address(t2).code.length, 0, "T2 did not deploy");
        assertGt(address(t3).code.length, 0, "T3 did not deploy");
    }

    function _deploy(
        bytes memory creationCode
    ) private returns (address deployed) {
        assembly ("memory-safe") {
            deployed := create(0, add(creationCode, 0x20), mload(creationCode))
        }
        require(deployed != address(0), "poseidon deployment failed");
    }
}
