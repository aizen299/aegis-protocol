# Solidity Domain Reference — Project Blockchain

## Foundry Project Layout

```
contracts/
├── src/
│   ├── vault/
│   │   ├── VaultEngine.sol
│   │   ├── YieldStrategy.sol
│   │   └── interfaces/IVaultEngine.sol
│   ├── oracle/
│   ├── governance/
│   ├── zk/
│   └── shared/
│       ├── access/
│       └── libraries/
├── test/
│   ├── unit/
│   ├── integration/
│   ├── fuzz/
│   └── invariant/
├── script/
└── foundry.toml
```

## Upgrade Pattern: UUPS

All upgradeable contracts use UUPS (ERC-1967). Rules:
- Inherit `UUPSUpgradeable` from OpenZeppelin.
- `initialize()` replaces `constructor()`, guarded with `initializer` modifier.
- `_authorizeUpgrade()` restricted to `UPGRADER_ROLE` (not owner-only).
- Storage layout must be append-only. Never remove or reorder variables.
- Use `StorageSlot` for unstructured storage when needed.
- Run `forge inspect <Contract> storage-layout` before every upgrade to verify compatibility.

```solidity
// Correct UUPS pattern
contract VaultEngine is
    Initializable,
    UUPSUpgradeable,
    AccessControlUpgradeable,
    ReentrancyGuardUpgradeable
{
    bytes32 public constant UPGRADER_ROLE = keccak256("UPGRADER_ROLE");
    bytes32 public constant VAULT_MANAGER_ROLE = keccak256("VAULT_MANAGER_ROLE");

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() { _disableInitializers(); }

    function initialize(address admin) external initializer {
        __UUPSUpgradeable_init();
        __AccessControl_init();
        __ReentrancyGuard_init();
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    function _authorizeUpgrade(address) internal override onlyRole(UPGRADER_ROLE) {}
}
```

## Access Control

- Use role-based access control (RBAC) via `AccessControlUpgradeable`.
- No `Ownable` for core contracts — too coarse-grained.
- Roles: `DEFAULT_ADMIN_ROLE`, `VAULT_MANAGER_ROLE`, `ORACLE_ROLE`, `GOVERNANCE_ROLE`, `UPGRADER_ROLE`.
- Admin key (DEFAULT_ADMIN_ROLE) must be a multisig (Safe) in production.
- Role grants/revocations must emit events. OpenZeppelin does this automatically.

## Reentrancy

- Follow CEI: Checks → Effects → Interactions.
- Apply `nonReentrant` from `ReentrancyGuardUpgradeable` on all external state-changing functions that transfer value or call external contracts.
- Do NOT rely solely on `transfer()` or `send()` for reentrancy protection — CEI + guard is mandatory.

```solidity
function withdraw(uint256 amount) external nonReentrant {
    // CHECKS
    require(balances[msg.sender] >= amount, "Insufficient balance");
    // EFFECTS
    balances[msg.sender] -= amount;
    totalDeposits -= amount;
    // INTERACTIONS
    IERC20(asset).safeTransfer(msg.sender, amount);
    emit Withdrawn(msg.sender, amount);
}
```

## Integer Safety

- Solidity 0.8.x: overflow/underflow revert by default.
- Use `unchecked {}` only where mathematically proven safe (loop counters, post-bound-checked arithmetic).
- For fixed-point math: use a battle-tested library (e.g., PRBMath or FixedPointMathLib from Solmate).
- No custom fixed-point math without thorough fuzz coverage.

## SafeERC20

Always use `SafeERC20` from OpenZeppelin for token interactions. Never call `.transfer()` or `.approve()` directly on IERC20 — non-standard tokens (USDT, etc.) will break.

```solidity
using SafeERC20 for IERC20;
// ...
IERC20(token).safeTransfer(recipient, amount);
IERC20(token).safeTransferFrom(sender, address(this), amount);
```

## Events

- Every state change emits an event.
- Events are the indexing surface for the Go backend. Design them carefully.
- Index fields that will be filtered: `indexed address user`, `indexed uint256 proposalId`.
- Event names: past tense (`Deposited`, `Withdrawn`, `ProposalCreated`, `VoteCast`).

```solidity
event Deposited(address indexed user, address indexed asset, uint256 amount, uint256 shares);
event Withdrawn(address indexed user, address indexed asset, uint256 amount, uint256 shares);
```

## Gas Optimization

- Pack storage structs: group variables by type to minimize slots.
- Cache storage reads in memory within loops: `uint256 len = arr.length`.
- Avoid dynamic arrays in storage when mappings suffice.
- `calldata` over `memory` for external function parameters that aren't mutated.
- Use `uint256` over smaller uints for single-slot values (EVM pads to 32 bytes anyway).
- Emit events instead of storing historical data on-chain.

## Testing Requirements (Foundry)

### Unit Tests
Every function: happy path + all revert cases.

### Fuzz Tests
```solidity
function testFuzz_deposit(uint256 amount) public {
    amount = bound(amount, 1, type(uint128).max);
    // ... test with arbitrary valid inputs
}
```

### Invariant Tests
```solidity
// Example invariant: total shares never exceed total assets
function invariant_sharesSolvency() public {
    assertLe(vault.totalShares(), vault.totalAssets());
}
```

### Integration Tests
Test full workflows across contract interactions. Deploy full system in setUp().

## Slither Checklist

Before any contract is complete, run:
```bash
slither src/ --config-file slither.config.json
```

Review and resolve or document all findings. Priority: HIGH and MEDIUM. LOW findings require documented justification to skip.

## Common Vulnerabilities to Check

| Vulnerability | Check |
|---|---|
| Reentrancy | CEI + nonReentrant |
| Price manipulation | No spot prices for decisions; use TWAP or medianization |
| Signature replay | Include chainId, nonce, contract address in signed data |
| Frontrunning | Use commit-reveal or slippage params where needed |
| DoS via gas | Avoid unbounded loops over user-supplied arrays |
| Storage collision | Verify slot layout before every upgrade |
| Initialization | `_disableInitializers()` in constructor; check `initialized` guard |
| Governance flash loan | Snapshot voting power at proposal creation block |
