// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

/// @notice Publishes governance actions for other chains through Wormhole (v2.0).
/// @dev See docs/v2.0-solana-plan.md §16.
interface IWormholeDispatcher {
    struct Route {
        uint16 wormholeChainId;
        bytes32 receiver;
    }

    event RouteSet(uint256 indexed targetChainId, uint16 wormholeChainId, bytes32 receiver);
    event RouteRemoved(uint256 indexed targetChainId);
    event MessageDispatched(
        uint256 indexed operationId, uint256 indexed targetChainId, uint16 wormholeChainId, uint64 sequence
    );

    error ZeroAddress();
    error NoRoute(uint256 targetChainId);
    error InvalidRoute(uint256 targetChainId);
    error LocalChain(uint256 targetChainId);
    error DoesNotFit(uint256 value);
    error WrongFee(uint256 sent, uint256 required);

    function dispatch(
        uint256 operationId,
        uint256 targetChainId,
        bytes32 target,
        uint256 value,
        bytes calldata payload
    ) external payable returns (uint64 sequence);

    function hasRoute(
        uint256 targetChainId
    ) external view returns (bool);

    function messageFee() external view returns (uint256);
}
