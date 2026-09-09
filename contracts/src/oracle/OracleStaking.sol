// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {
    AccessControlUpgradeable
} from "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";
import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import {
    ReentrancyGuardUpgradeable
} from "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";
import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {SafeERC20} from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import {Math} from "@openzeppelin/contracts/utils/math/Math.sol";

import {Roles} from "../shared/access/Roles.sol";
import {IOracleStaking} from "./interfaces/IOracleStaking.sol";

/// @title OracleStaking (v0.2)
/// @notice Registration, stake, and slashing for the oracle node set.
/// @dev Storage layout is append-only. Run `make contracts-layout-check` before any upgrade.
contract OracleStaking is
    Initializable,
    UUPSUpgradeable,
    AccessControlUpgradeable,
    ReentrancyGuardUpgradeable,
    IOracleStaking
{
    using SafeERC20 for IERC20;

    uint256 private constant _BPS_DENOMINATOR = 10_000;

    /// @dev Slashing is decided off-chain after a round settles, so stake must stay locked longer
    ///      than that decision takes. Anything shorter makes every penalty optional for a node that
    ///      unstakes on time. See docs/v0.2-oracle-plan.md §2.3.
    uint256 private constant _MIN_UNBONDING_PERIOD = 1 days;

    struct NodeInfo {
        uint256 stake;
        uint256 pendingUnstake;
        uint256 claimableAt;
        uint256 slashedTotal;
        uint256 activatedAtVersion;
        bool registered;
        bool active;
    }

    // --- storage (append-only) ---
    IERC20 private _stakeToken;
    uint256 private _minimumStake;
    uint256 private _minStakeFloor;
    uint256 private _unbondingPeriod;
    uint256 private _maxSlashBps;
    uint256 private _maxNodes;
    uint256 private _activeNodeCount;
    uint256 private _nodeSetVersion;
    mapping(address node => NodeInfo info) private _nodes;
    mapping(uint256 roundId => mapping(address node => bool slashed)) private _slashedInRound;

    // slither-disable-next-line unused-state
    uint256[40] private __gap;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(
        address admin,
        address stakeToken_,
        uint256 minimumStake_,
        uint256 minStakeFloor_,
        uint256 unbondingPeriod_,
        uint256 maxSlashBps_,
        uint256 maxNodes_
    ) external initializer {
        if (admin == address(0) || stakeToken_ == address(0)) revert ZeroAddress();
        if (maxSlashBps_ == 0 || maxSlashBps_ > _BPS_DENOMINATOR) revert InvalidBps(maxSlashBps_);
        if (unbondingPeriod_ < _MIN_UNBONDING_PERIOD) {
            revert UnbondingPeriodTooShort(unbondingPeriod_, _MIN_UNBONDING_PERIOD);
        }
        if (minStakeFloor_ > minimumStake_) revert StakeBelowMinimum(minimumStake_, minStakeFloor_);
        if (maxNodes_ == 0) revert ZeroAmount();

        __UUPSUpgradeable_init();
        __AccessControl_init();
        __ReentrancyGuard_init();

        _stakeToken = IERC20(stakeToken_);
        _minimumStake = minimumStake_;
        _minStakeFloor = minStakeFloor_;
        _unbondingPeriod = unbondingPeriod_;
        _maxSlashBps = maxSlashBps_;
        _maxNodes = maxNodes_;

        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    // --- node actions ---

    /// @inheritdoc IOracleStaking
    function register(
        uint256 amount
    ) external nonReentrant {
        NodeInfo storage node = _nodes[msg.sender];
        if (node.registered) revert AlreadyRegistered(msg.sender);
        if (amount < _minimumStake) revert StakeBelowMinimum(amount, _minimumStake);
        if (_activeNodeCount >= _maxNodes) revert NodeSetFull(_maxNodes);

        node.registered = true;
        node.stake = amount;
        _activate(msg.sender, node);

        _stakeToken.safeTransferFrom(msg.sender, address(this), amount);

        emit NodeRegistered(msg.sender, amount);
        emit NodeStaked(msg.sender, amount, amount);
    }

    /// @inheritdoc IOracleStaking
    function stake(
        uint256 amount
    ) external nonReentrant {
        NodeInfo storage node = _nodes[msg.sender];
        if (!node.registered) revert NotRegistered(msg.sender);
        if (amount == 0) revert ZeroAmount();

        node.stake += amount;
        _stakeToken.safeTransferFrom(msg.sender, address(this), amount);

        // Topping back above the floor reactivates a node that was deactivated for being under it.
        if (!node.active && node.pendingUnstake == 0 && node.stake >= _minimumStake) {
            _activate(msg.sender, node);
        }

        emit NodeStaked(msg.sender, amount, node.stake);
    }

    /// @inheritdoc IOracleStaking
    /// @dev Deactivates immediately, and the stake stays slashable for the whole unbonding period.
    ///      Without that, a node could submit bad data, watch the round settle, and withdraw before
    ///      the backend's slash lands — which would make every penalty in the schedule optional.
    function requestUnstake(
        uint256 amount
    ) external nonReentrant {
        NodeInfo storage node = _nodes[msg.sender];
        if (!node.registered) revert NotRegistered(msg.sender);
        if (amount == 0) revert ZeroAmount();
        if (node.pendingUnstake != 0) revert UnstakeAlreadyRequested(msg.sender);
        if (amount > node.stake) revert InsufficientStake(node.stake, amount);

        node.pendingUnstake = amount;
        node.claimableAt = block.timestamp + _unbondingPeriod;

        if (node.active) _deactivate(msg.sender, node, "UNSTAKE_REQUESTED");

        emit UnstakeRequested(msg.sender, amount, node.claimableAt);
    }

    /// @inheritdoc IOracleStaking
    function cancelUnstake() external nonReentrant {
        NodeInfo storage node = _nodes[msg.sender];
        if (node.pendingUnstake == 0) revert NoUnstakeRequested(msg.sender);

        uint256 cancelled = node.pendingUnstake;
        node.pendingUnstake = 0;
        node.claimableAt = 0;

        if (node.stake >= _minimumStake) _activate(msg.sender, node);

        emit UnstakeCancelled(msg.sender, cancelled);
    }

    /// @inheritdoc IOracleStaking
    /// @dev A slash during unbonding can leave the stake below the requested amount, in which case
    ///      only what remains is released. The slash takes precedence over the request.
    function completeUnstake() external nonReentrant {
        NodeInfo storage node = _nodes[msg.sender];
        if (node.pendingUnstake == 0) revert NoUnstakeRequested(msg.sender);
        // Sequencer timestamp drift is seconds-scale; the unbonding period is days. Shifting a
        // 7-day deadline by a few seconds buys an exiting node nothing. A block count would be the
        // unstable unit here — see docs/v0.2-oracle-plan.md §2.1.
        // slither-disable-next-line timestamp
        if (block.timestamp < node.claimableAt) {
            revert UnbondingNotElapsed(node.claimableAt, block.timestamp);
        }

        uint256 amount = Math.min(node.pendingUnstake, node.stake);

        node.pendingUnstake = 0;
        node.claimableAt = 0;
        node.stake -= amount;

        if (amount != 0) _stakeToken.safeTransfer(msg.sender, amount);

        emit NodeUnstaked(msg.sender, amount, node.stake);
    }

    // --- slashing ---

    /// @notice Slash a node's stake. Called by the backend after a round settles.
    /// @dev SLASHER_ROLE is a hot key by design, so the ceiling is enforced here rather than
    ///      trusted to the caller: the backend decides whether to slash, the contract decides what
    ///      is survivable. Slashed stake stays in the contract; disposition is a v0.3 governance
    ///      decision.
    function slash(
        address node_,
        uint256 roundId,
        uint256 amount,
        bytes32 reason
    ) external nonReentrant onlyRole(Roles.SLASHER_ROLE) returns (uint256 slashed) {
        NodeInfo storage node = _nodes[node_];
        if (!node.registered) revert NotRegistered(node_);
        if (amount == 0) revert ZeroAmount();
        if (roundId == 0) revert InvalidRound(roundId);

        // One penalty per node per round. This is what makes the backend's retry-after-crash safe:
        // the executor cannot know whether a transaction it lost track of landed, so a second
        // attempt must revert rather than take the stake twice.
        if (_slashedInRound[roundId][node_]) revert AlreadySlashedForRound(node_, roundId);

        uint256 cap = Math.mulDiv(node.stake, _maxSlashBps, _BPS_DENOMINATOR);
        if (amount > cap) revert SlashExceedsCap(amount, cap);

        _slashedInRound[roundId][node_] = true;
        slashed = Math.min(amount, node.stake);
        node.stake -= slashed;
        node.slashedTotal += slashed;

        if (node.active && node.stake < _minStakeFloor) {
            _deactivate(node_, node, "BELOW_STAKE_FLOOR");
        }

        emit NodeSlashed(node_, roundId, slashed, reason, node.stake);
    }

    // --- admin ---

    function deactivate(
        address node_,
        bytes32 reason
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        NodeInfo storage node = _nodes[node_];
        if (!node.registered) revert NotRegistered(node_);
        if (!node.active) revert NodeInactive(node_);

        _deactivate(node_, node, reason);
    }

    function setMinimumStake(
        uint256 value
    ) external onlyRole(Roles.ORACLE_MANAGER_ROLE) {
        if (value < _minStakeFloor) revert StakeBelowMinimum(value, _minStakeFloor);
        emit MinimumStakeUpdated(_minimumStake, value);
        _minimumStake = value;
    }

    function setMinStakeFloor(
        uint256 value
    ) external onlyRole(Roles.ORACLE_MANAGER_ROLE) {
        if (value > _minimumStake) revert StakeBelowMinimum(_minimumStake, value);
        emit MinStakeFloorUpdated(_minStakeFloor, value);
        _minStakeFloor = value;
    }

    /// @dev Shortening this is how a node escapes a pending slash, so the floor is enforced here
    ///      and the change is gated on the admin multisig rather than the manager key.
    function setUnbondingPeriod(
        uint256 value
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        if (value < _MIN_UNBONDING_PERIOD) {
            revert UnbondingPeriodTooShort(value, _MIN_UNBONDING_PERIOD);
        }
        emit UnbondingPeriodUpdated(_unbondingPeriod, value);
        _unbondingPeriod = value;
    }

    function setMaxNodes(
        uint256 value
    ) external onlyRole(Roles.ORACLE_MANAGER_ROLE) {
        if (value == 0) revert ZeroAmount();
        emit MaxNodesUpdated(_maxNodes, value);
        _maxNodes = value;
    }

    // --- views ---

    function stakeOf(
        address node_
    ) public view returns (uint256) {
        return _nodes[node_].stake;
    }

    function isActive(
        address node_
    ) public view returns (bool) {
        return _nodes[node_].active;
    }

    function isRegistered(
        address node_
    ) public view returns (bool) {
        return _nodes[node_].registered;
    }

    function activeNodeCount() public view returns (uint256) {
        return _activeNodeCount;
    }

    function nodeSetVersion() public view returns (uint256) {
        return _nodeSetVersion;
    }

    function isEligibleAt(
        address node_,
        uint256 version
    ) public view returns (bool) {
        NodeInfo storage node = _nodes[node_];
        return node.active && node.activatedAtVersion <= version;
    }

    function nodeInfo(
        address node_
    ) public view returns (NodeInfo memory) {
        return _nodes[node_];
    }

    function stakeToken() public view returns (address) {
        return address(_stakeToken);
    }

    function minimumStake() public view returns (uint256) {
        return _minimumStake;
    }

    function minStakeFloor() public view returns (uint256) {
        return _minStakeFloor;
    }

    function unbondingPeriod() public view returns (uint256) {
        return _unbondingPeriod;
    }

    function maxSlashBps() public view returns (uint256) {
        return _maxSlashBps;
    }

    function maxNodes() public view returns (uint256) {
        return _maxNodes;
    }

    /// @notice Stake that is not spoken for by a pending unstake request.
    /// @notice Whether a node has already been penalised for a round.
    function slashedInRound(
        uint256 roundId,
        address node_
    ) public view returns (bool) {
        return _slashedInRound[roundId][node_];
    }

    function slashableStake(
        address node_
    ) public view returns (uint256) {
        return _nodes[node_].stake;
    }

    // --- internals ---

    function _activate(
        address node_,
        NodeInfo storage node
    ) private {
        node.active = true;
        _activeNodeCount += 1;
        _nodeSetVersion += 1;
        node.activatedAtVersion = _nodeSetVersion;
        emit NodeReactivated(node_);
    }

    function _deactivate(
        address node_,
        NodeInfo storage node,
        bytes32 reason
    ) private {
        node.active = false;
        _activeNodeCount -= 1;
        _nodeSetVersion += 1;
        emit NodeDeactivated(node_, reason);
    }

    function _authorizeUpgrade(
        address
    ) internal override onlyRole(Roles.UPGRADER_ROLE) {}
}
