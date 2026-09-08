// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {
    AccessControlUpgradeable
} from "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";
import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import {PausableUpgradeable} from "@openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol";
import {
    ReentrancyGuardUpgradeable
} from "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";
import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {SafeERC20} from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import {Math} from "@openzeppelin/contracts/utils/math/Math.sol";

import {Roles} from "../shared/access/Roles.sol";
import {IVaultEngine} from "./interfaces/IVaultEngine.sol";
import {IYieldStrategy} from "./interfaces/IYieldStrategy.sol";

/// @title VaultEngine (v0.1)
/// @notice Single-asset vault with internal share accounting, one optional yield strategy, and
///         manager-configurable risk controls.
/// @dev Shares are non-transferable in v0.1 — accounting only, no ERC-20 surface.
///      Storage layout is append-only. Run `forge inspect VaultEngine storage-layout` before any
///      upgrade and diff against the previous release.
contract VaultEngine is
    Initializable,
    UUPSUpgradeable,
    AccessControlUpgradeable,
    ReentrancyGuardUpgradeable,
    PausableUpgradeable,
    IVaultEngine
{
    using SafeERC20 for IERC20;

    uint256 private constant _BPS_DENOMINATOR = 10_000;

    /// @dev Virtual shares/assets offset. Makes the first-depositor share-inflation attack
    ///      cost more than it can extract. See OpenZeppelin ERC-4626 inflation-attack notes.
    uint8 private constant _VIRTUAL_SHARES_OFFSET = 3;
    uint256 private constant _VIRTUAL_SHARES = 10 ** _VIRTUAL_SHARES_OFFSET;
    uint256 private constant _VIRTUAL_ASSETS = 1;

    // --- storage (append-only) ---
    address private _asset;
    bool private _withdrawalsFrozen;
    IYieldStrategy private _strategy;
    uint256 private _totalShares;
    uint256 private _depositCap;
    uint256 private _minDeposit;
    uint256 private _maxStrategyAllocationBps;
    mapping(address account => uint256 shares) private _sharesOf;

    // slither-disable-next-line unused-state
    uint256[42] private __gap;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    /// @param admin Holder of DEFAULT_ADMIN_ROLE. Must be a multisig in production.
    /// @param asset_ Underlying ERC-20. Immutable for the life of the vault.
    /// @param depositCap_ Ceiling on totalAssets. Zero disables the cap.
    /// @param minDeposit_ Minimum assets per deposit call. Guards dust-rounding griefing.
    /// @param maxStrategyAllocationBps_ Share of totalAssets the strategy may hold, in bps.
    function initialize(
        address admin,
        address asset_,
        uint256 depositCap_,
        uint256 minDeposit_,
        uint256 maxStrategyAllocationBps_
    ) external initializer {
        if (admin == address(0) || asset_ == address(0)) revert ZeroAddress();
        if (maxStrategyAllocationBps_ > _BPS_DENOMINATOR) revert InvalidBps(maxStrategyAllocationBps_);

        __UUPSUpgradeable_init();
        __AccessControl_init();
        __ReentrancyGuard_init();
        __Pausable_init();

        _asset = asset_;
        _depositCap = depositCap_;
        _minDeposit = minDeposit_;
        _maxStrategyAllocationBps = maxStrategyAllocationBps_;

        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    // --- user actions ---

    /// @inheritdoc IVaultEngine
    /// @dev Shares are credited on assets actually received, so fee-on-transfer tokens do not
    ///      over-mint. The external transfer precedes the effects out of necessity; `nonReentrant`
    ///      is the reentrancy control here.
    function deposit(
        uint256 assets,
        address receiver
    ) external nonReentrant whenNotPaused returns (uint256 shares) {
        if (receiver == address(0)) revert ZeroAddress();
        if (assets == 0) revert ZeroAmount();

        uint256 minimum = _minDeposit;
        if (assets < minimum) revert DepositBelowMinimum(assets, minimum);

        IERC20 token = IERC20(_asset);
        uint256 assetsBefore = totalAssets();

        uint256 balanceBefore = token.balanceOf(address(this));
        token.safeTransferFrom(msg.sender, address(this), assets);
        uint256 received = token.balanceOf(address(this)) - balanceBefore;

        uint256 cap = _depositCap;
        if (cap != 0 && assetsBefore + received > cap) {
            revert DepositCapExceeded(assetsBefore + received, cap);
        }

        shares = _convertToShares(received, assetsBefore, Math.Rounding.Floor);
        // slither-disable-next-line incorrect-equality
        if (shares == 0) revert ZeroAmount();

        _totalShares += shares;
        _sharesOf[receiver] += shares;

        emit Deposited(receiver, address(token), received, shares);
    }

    /// @inheritdoc IVaultEngine
    /// @dev Burns `shares` from the caller and pays `receiver`. Pulls from the strategy only if
    ///      the idle balance is short.
    function withdraw(
        uint256 shares,
        address receiver
    ) external nonReentrant returns (uint256 assets) {
        if (_withdrawalsFrozen) revert WithdrawalsFrozen();
        if (receiver == address(0)) revert ZeroAddress();
        if (shares == 0) revert ZeroAmount();

        uint256 held = _sharesOf[msg.sender];
        if (held < shares) revert InsufficientShares(held, shares);

        assets = _convertToAssets(shares, totalAssets(), Math.Rounding.Floor);
        // slither-disable-next-line incorrect-equality
        if (assets == 0) revert ZeroAmount();

        _sharesOf[msg.sender] = held - shares;
        _totalShares -= shares;

        IERC20 token = IERC20(_asset);
        _ensureLiquidity(token, assets);
        token.safeTransfer(receiver, assets);
        emit Withdrawn(msg.sender, address(token), assets, shares);
    }

    // --- strategy management ---

    /// @notice Point the vault at a new yield strategy. The current strategy must be fully unwound.
    function setStrategy(
        address newStrategy
    ) external onlyRole(Roles.VAULT_MANAGER_ROLE) {
        IYieldStrategy previous = _strategy;
        if (address(previous) != address(0)) {
            uint256 remaining = previous.totalAssets();
            if (remaining != 0) revert StrategyStillFunded(remaining);
        }

        if (newStrategy != address(0)) {
            address strategyAsset = IYieldStrategy(newStrategy).asset();
            if (strategyAsset != _asset) revert StrategyAssetMismatch(strategyAsset, _asset);

            address strategyVault = IYieldStrategy(newStrategy).vault();
            if (strategyVault != address(this)) revert StrategyVaultMismatch(strategyVault, address(this));
        }

        _strategy = IYieldStrategy(newStrategy);
        emit StrategyUpdated(address(previous), newStrategy);
    }

    /// @notice Deploy idle assets into the active strategy, subject to the allocation cap.
    /// @dev The cap binds at allocation time only. Withdrawals drain the idle buffer, so the
    ///      realised allocated/total ratio can drift above it; forcing a deallocation on every
    ///      withdrawal would let an illiquid venue block user exits.
    function allocate(
        uint256 amount
    ) external nonReentrant whenNotPaused onlyRole(Roles.VAULT_MANAGER_ROLE) {
        IYieldStrategy strategy_ = _strategy;
        if (address(strategy_) == address(0)) revert ZeroAddress();
        if (amount == 0) revert ZeroAmount();

        IERC20 token = IERC20(_asset);
        uint256 idle = token.balanceOf(address(this));
        if (idle < amount) revert InsufficientLiquidity(idle, amount);

        token.forceApprove(address(strategy_), amount);
        strategy_.deposit(amount);
        token.forceApprove(address(strategy_), 0);

        uint256 allocated = strategy_.totalAssets();
        uint256 cap = Math.mulDiv(totalAssets(), _maxStrategyAllocationBps, _BPS_DENOMINATOR);
        if (allocated > cap) revert AllocationCapExceeded(allocated, cap);

        emit AllocatedToStrategy(address(strategy_), amount);
    }

    /// @notice Recall assets from the active strategy into the idle buffer.
    function deallocate(
        uint256 amount
    ) external nonReentrant onlyRole(Roles.VAULT_MANAGER_ROLE) {
        IYieldStrategy strategy_ = _strategy;
        if (address(strategy_) == address(0)) revert ZeroAddress();
        if (amount == 0) revert ZeroAmount();

        uint256 withdrawn = strategy_.withdraw(amount);
        emit DeallocatedFromStrategy(address(strategy_), withdrawn);
    }

    /// @notice Unwind the entire strategy position. Intended for incident response.
    function emergencyDeallocateAll()
        external
        nonReentrant
        onlyRole(Roles.VAULT_MANAGER_ROLE)
        returns (uint256 withdrawn)
    {
        IYieldStrategy strategy_ = _strategy;
        if (address(strategy_) == address(0)) revert ZeroAddress();

        withdrawn = strategy_.emergencyWithdrawAll();
        emit DeallocatedFromStrategy(address(strategy_), withdrawn);
    }

    // --- risk controls ---

    function setDepositCap(
        uint256 newCap
    ) external onlyRole(Roles.VAULT_MANAGER_ROLE) {
        emit DepositCapUpdated(_depositCap, newCap);
        _depositCap = newCap;
    }

    function setMinDeposit(
        uint256 newMin
    ) external onlyRole(Roles.VAULT_MANAGER_ROLE) {
        emit MinDepositUpdated(_minDeposit, newMin);
        _minDeposit = newMin;
    }

    function setMaxStrategyAllocationBps(
        uint256 newBps
    ) external onlyRole(Roles.VAULT_MANAGER_ROLE) {
        if (newBps > _BPS_DENOMINATOR) revert InvalidBps(newBps);
        emit MaxStrategyAllocationUpdated(_maxStrategyAllocationBps, newBps);
        _maxStrategyAllocationBps = newBps;
    }

    /// @notice Halts deposits and allocation. Withdrawals stay open by design.
    function pause() external onlyRole(Roles.PAUSER_ROLE) {
        _pause();
    }

    function unpause() external onlyRole(Roles.PAUSER_ROLE) {
        _unpause();
    }

    /// @notice Halts withdrawals. Gated on DEFAULT_ADMIN_ROLE rather than PAUSER_ROLE because it
    ///         traps user funds — a strictly higher bar than pausing deposits.
    function setWithdrawalsFrozen(
        bool frozen
    ) external onlyRole(DEFAULT_ADMIN_ROLE) {
        _withdrawalsFrozen = frozen;
        emit WithdrawalsFrozenSet(frozen);
    }

    // --- views ---

    function asset() public view returns (address) {
        return _asset;
    }

    function idleAssets() public view returns (uint256) {
        return IERC20(_asset).balanceOf(address(this));
    }

    function allocatedAssets() public view returns (uint256) {
        IYieldStrategy strategy_ = _strategy;
        return address(strategy_) == address(0) ? 0 : strategy_.totalAssets();
    }

    function totalAssets() public view returns (uint256) {
        return idleAssets() + allocatedAssets();
    }

    function totalShares() public view returns (uint256) {
        return _totalShares;
    }

    function sharesOf(
        address account
    ) public view returns (uint256) {
        return _sharesOf[account];
    }

    function convertToShares(
        uint256 assets
    ) public view returns (uint256) {
        return _convertToShares(assets, totalAssets(), Math.Rounding.Floor);
    }

    function convertToAssets(
        uint256 shares
    ) public view returns (uint256) {
        return _convertToAssets(shares, totalAssets(), Math.Rounding.Floor);
    }

    function strategy() public view returns (address) {
        return address(_strategy);
    }

    function depositCap() public view returns (uint256) {
        return _depositCap;
    }

    function minDeposit() public view returns (uint256) {
        return _minDeposit;
    }

    function maxStrategyAllocationBps() public view returns (uint256) {
        return _maxStrategyAllocationBps;
    }

    function withdrawalsFrozen() public view returns (bool) {
        return _withdrawalsFrozen;
    }

    /// @notice Decimal offset the virtual-shares defence applies. A share is scaled by
    ///         10**offset relative to the asset, so integrators rendering share amounts use
    ///         `assetDecimals + virtualSharesOffset()` rather than assuming a scale.
    function virtualSharesOffset() public pure returns (uint8) {
        return _VIRTUAL_SHARES_OFFSET;
    }

    // --- internals ---

    function _convertToShares(
        uint256 assets,
        uint256 totalAssets_,
        Math.Rounding rounding
    ) private view returns (uint256) {
        return Math.mulDiv(assets, _totalShares + _VIRTUAL_SHARES, totalAssets_ + _VIRTUAL_ASSETS, rounding);
    }

    function _convertToAssets(
        uint256 shares,
        uint256 totalAssets_,
        Math.Rounding rounding
    ) private view returns (uint256) {
        return Math.mulDiv(shares, totalAssets_ + _VIRTUAL_ASSETS, _totalShares + _VIRTUAL_SHARES, rounding);
    }

    /// @dev Tops the idle balance up to `assets` from the strategy if it is short.
    function _ensureLiquidity(
        IERC20 token,
        uint256 assets
    ) private {
        uint256 idle = token.balanceOf(address(this));
        if (idle >= assets) return;

        _pullFromStrategy(assets - idle);

        uint256 available = token.balanceOf(address(this));
        if (available < assets) revert InsufficientLiquidity(available, assets);
    }

    function _pullFromStrategy(
        uint256 amount
    ) private {
        IYieldStrategy strategy_ = _strategy;
        if (address(strategy_) == address(0)) return;

        uint256 withdrawn = strategy_.withdraw(amount);
        if (withdrawn != 0) emit DeallocatedFromStrategy(address(strategy_), withdrawn);
    }

    function _authorizeUpgrade(
        address
    ) internal override onlyRole(Roles.UPGRADER_ROLE) {}
}
