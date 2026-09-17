// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice The two calls the dispatcher makes on Wormhole's core contract.
interface IWormhole {
    function publishMessage(
        uint32 nonce,
        bytes memory payload,
        uint8 consistencyLevel
    ) external payable returns (uint64 sequence);

    function messageFee() external view returns (uint256);
}
