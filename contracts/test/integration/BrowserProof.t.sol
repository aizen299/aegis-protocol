// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {Test} from "forge-std/Test.sol";

import {HonkVerifier} from "../../generated/HonkVerifier.sol";
import {IZkVerifier} from "../../src/zk/verifier/IZkVerifier.sol";

/// @title A proof made by the browser path must satisfy the committed verifier.
/// @dev Two proving implementations have to agree: the Rust service shells out to the nargo and bb
///      binaries, the browser uses noir_js and bb.js. Nothing makes them agree except this.
///
///      Not part of `forge test` — it needs an artifact generated first. Run
///      `make zk-browser-proof-check`, which generates and then runs it.
contract BrowserProofTest is Test {
    string internal constant PROOF_PATH = "./test/artifacts/browser-proof.hex";
    string internal constant INPUTS_PATH = "./test/artifacts/browser-public-inputs.hex";

    function test_aBrowserGeneratedProofVerifies() public {
        bytes memory proof = vm.parseBytes(vm.trim(vm.readFile(PROOF_PATH)));
        string[] memory raw = vm.split(vm.trim(vm.readFile(INPUTS_PATH)), " ");

        // An absent or truncated artifact must fail as absent, not pass as verified.
        assertGt(proof.length, 0, "the browser proof artifact is empty");
        assertEq(raw.length, 6, "expected 6 public inputs from the browser path");

        bytes32[] memory publicInputs = new bytes32[](raw.length);
        for (uint256 i = 0; i < raw.length; i++) {
            publicInputs[i] = vm.parseBytes32(raw[i]);
        }

        IZkVerifier verifier = IZkVerifier(address(new HonkVerifier()));
        assertTrue(
            verifier.verify(proof, publicInputs),
            "the committed verifier rejected a proof from the browser proving path"
        );
    }
}
