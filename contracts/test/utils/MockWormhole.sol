// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {IWormhole} from "../../src/bridge/interfaces/IWormhole.sol";

/// @dev Records what Wormhole's core would publish, and charges a settable fee.
contract MockWormhole is IWormhole {
    uint256 public fee;
    uint64 public nextSequence;
    bytes public lastPayload;
    uint32 public lastNonce;
    uint8 public lastConsistency;
    address public lastEmitter;
    uint256 public published;

    error WrongFee();

    function setFee(
        uint256 value
    ) external {
        fee = value;
    }

    function publishMessage(
        uint32 nonce,
        bytes memory payload,
        uint8 consistencyLevel
    ) external payable returns (uint64 sequence) {
        if (msg.value != fee) revert WrongFee();
        sequence = nextSequence++;
        lastPayload = payload;
        lastNonce = nonce;
        lastConsistency = consistencyLevel;
        lastEmitter = msg.sender;
        published += 1;
    }

    function messageFee() external view returns (uint256) {
        return fee;
    }
}
