// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {
    AccessControlUpgradeable
} from "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";
import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

import {Roles} from "../shared/access/Roles.sol";
import {IWormhole} from "./interfaces/IWormhole.sol";
import {IWormholeDispatcher} from "./interfaces/IWormholeDispatcher.sol";

/// @title WormholeDispatcher (v2.0)
/// @notice Publishes a governance action for another chain as a Wormhole message. The Timelock is its
///         only caller; the delay a proposal waited out is enforced there, not here.
/// @dev Storage layout is append-only. See docs/v2.0-solana-plan.md §16.
contract WormholeDispatcher is Initializable, UUPSUpgradeable, AccessControlUpgradeable, IWormholeDispatcher {
    uint8 public constant MESSAGE_VERSION = 1;
    /// @dev Wormhole's "finalized" consistency level: guardians sign only after Arbitrum finality.
    uint8 public constant CONSISTENCY_FINALIZED = 1;

    // --- storage (append-only) ---
    IWormhole private _wormhole;
    mapping(uint256 targetChainId => Route route) private _routes;

    // slither-disable-next-line unused-state
    uint256[48] private __gap;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(
        address admin,
        address wormhole_
    ) external initializer {
        if (admin == address(0) || wormhole_ == address(0)) revert ZeroAddress();
        __UUPSUpgradeable_init();
        __AccessControl_init();
        _wormhole = IWormhole(wormhole_);
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    /// @inheritdoc IWormholeDispatcher
    /// @dev The fee is paid by the caller, exactly: an overpayment would sit in this contract with no
    ///      way out, and an underpayment would revert inside Wormhole with a less useful error.
    function dispatch(
        uint256 operationId,
        uint256 targetChainId,
        bytes32 target,
        uint256 value,
        bytes calldata payload
    ) external payable onlyRole(Roles.DISPATCHER_CALLER_ROLE) returns (uint64 sequence) {
        Route memory route = _routes[targetChainId];
        if (route.wormholeChainId == 0) revert NoRoute(targetChainId);

        uint256 fee = _wormhole.messageFee();
        if (msg.value != fee) revert WrongFee(msg.value, fee);

        bytes memory message = encodeMessage(operationId, targetChainId, target, value, payload);
        // The recipient is Wormhole's core, fixed at initialization, and the amount is exactly the fee it
        // charges: not an arbitrary destination.
        // slither-disable-next-line arbitrary-send-eth
        sequence = _wormhole.publishMessage{value: fee}(0, message, CONSISTENCY_FINALIZED);

        // The event carries the sequence Wormhole assigned, so it cannot precede the call. The callee is
        // the core address fixed at initialization.
        // slither-disable-next-line reentrancy-events
        emit MessageDispatched(operationId, targetChainId, route.wormholeChainId, sequence);
    }

    /// @notice The version-1 message: version, source chain, operation, target chain, target, value,
    ///         then the payload, packed big-endian. §16.3.
    /// @dev Chain ids and the operation id are 8 bytes. Every registry id fits, and a value that would
    ///      not is refused rather than truncated into another chain's id.
    function encodeMessage(
        uint256 operationId,
        uint256 targetChainId,
        bytes32 target,
        uint256 value,
        bytes calldata payload
    ) public view returns (bytes memory) {
        if (operationId > type(uint64).max) revert DoesNotFit(operationId);
        if (targetChainId > type(uint64).max) revert DoesNotFit(targetChainId);
        if (block.chainid > type(uint64).max) revert DoesNotFit(block.chainid);
        return abi.encodePacked(
            MESSAGE_VERSION,
            uint64(block.chainid),
            uint64(operationId),
            uint64(targetChainId),
            target,
            value,
            payload
        );
    }

    // --- routes ---

    function setRoute(
        uint256 targetChainId,
        uint16 wormholeChainId,
        bytes32 receiver
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        if (targetChainId == block.chainid) revert LocalChain(targetChainId);
        if (targetChainId > type(uint64).max || wormholeChainId == 0 || receiver == bytes32(0)) {
            revert InvalidRoute(targetChainId);
        }
        _routes[targetChainId] = Route({wormholeChainId: wormholeChainId, receiver: receiver});
        emit RouteSet(targetChainId, wormholeChainId, receiver);
    }

    function removeRoute(
        uint256 targetChainId
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        if (_routes[targetChainId].wormholeChainId == 0) revert NoRoute(targetChainId);
        delete _routes[targetChainId];
        emit RouteRemoved(targetChainId);
    }

    // --- views ---

    /// @inheritdoc IWormholeDispatcher
    function hasRoute(
        uint256 targetChainId
    ) external view returns (bool) {
        return _routes[targetChainId].wormholeChainId != 0;
    }

    function routeOf(
        uint256 targetChainId
    ) external view returns (Route memory) {
        return _routes[targetChainId];
    }

    /// @inheritdoc IWormholeDispatcher
    function messageFee() external view returns (uint256) {
        return _wormhole.messageFee();
    }

    function wormhole() external view returns (address) {
        return address(_wormhole);
    }

    function _authorizeUpgrade(
        address
    ) internal override onlyRole(Roles.UPGRADER_ROLE) {}
}
