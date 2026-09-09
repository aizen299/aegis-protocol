// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {Test} from "forge-std/Test.sol";

import {HonkVerifier} from "../../generated/HonkVerifier.sol";
import {IZkVerifier} from "../../src/zk/verifier/IZkVerifier.sol";
import {ProofFixture} from "../utils/ProofFixture.sol";

/// @title The generated verifier accepts a real proof and rejects everything else.
/// @dev docs/zk.md requires "integration tests with valid/invalid proofs". A verifier that returns
///      true unconditionally passes every positive test ever written, so the negatives are the
///      substance here.
contract ZkVerifierTest is Test {
    IZkVerifier internal verifier;

    function setUp() public {
        // Constructed rather than deployed from committed bytecode: the generated verifier links
        // two external libraries, so its creation code carries unlinked placeholders and cannot be
        // deployed as a byte string the way Poseidon is. Foundry links them.
        verifier = IZkVerifier(address(new HonkVerifier()));
    }

    function test_aRealProofVerifies() public view {
        assertTrue(
            verifier.verify(ProofFixture.PROOF, ProofFixture.publicInputs()),
            "the generated verifier rejected a proof bb accepts"
        );
    }

    /// Every public input is bound. Changing any one must invalidate the proof, or that input is
    /// decoration and an attacker chooses it freely.
    function test_changingAnyPublicInputInvalidatesTheProof() public view {
        for (uint256 i = 0; i < 5; i++) {
            bytes32[] memory tampered = ProofFixture.publicInputs();
            tampered[i] = bytes32(uint256(tampered[i]) + 1);

            (bool ok, bytes memory result) = address(verifier)
                .staticcall(abi.encodeCall(IZkVerifier.verify, (ProofFixture.PROOF, tampered)));
            bool verified = ok && result.length == 32 && abi.decode(result, (bool));

            assertFalse(verified, "a public input is not bound to the proof");
        }
    }

    function test_aTamperedProofIsRejected() public view {
        bytes memory tampered = ProofFixture.PROOF;
        tampered[64] = bytes1(uint8(tampered[64]) ^ 0xff);

        (bool ok, bytes memory result) = address(verifier)
            .staticcall(abi.encodeCall(IZkVerifier.verify, (tampered, ProofFixture.publicInputs())));
        assertFalse(ok && abi.decode(result, (bool)), "a tampered proof verified");
    }

    function test_anEmptyProofIsRejected() public view {
        (bool ok, bytes memory result) = address(verifier)
            .staticcall(abi.encodeCall(IZkVerifier.verify, ("", ProofFixture.publicInputs())));
        assertFalse(ok && result.length == 32 && abi.decode(result, (bool)), "an empty proof verified");
    }

    function test_theWrongNumberOfPublicInputsIsRejected() public view {
        bytes32[] memory tooFew = new bytes32[](4);
        for (uint256 i = 0; i < 4; i++) {
            tooFew[i] = ProofFixture.publicInputs()[i];
        }

        (bool ok, bytes memory result) =
            address(verifier).staticcall(abi.encodeCall(IZkVerifier.verify, (ProofFixture.PROOF, tooFew)));
        assertFalse(ok && result.length == 32 && abi.decode(result, (bool)), "a short input list verified");
    }

    /// The fixture's chain id is the one the witness was built with. The gate address is asserted
    /// in ZkVaultGate.t.sol, where the gate that must match it is actually deployed.
    function test_theFixtureCarriesTheExpectedDomain() public pure {
        assertEq(uint256(ProofFixture.CHAIN_ID), 31337, "fixture chain id changed");
        assertEq(uint256(ProofFixture.ACTION_ID), 7, "fixture action id changed");
        assertTrue(uint256(ProofFixture.GATE) != 0, "fixture carries no gate");
    }
}
