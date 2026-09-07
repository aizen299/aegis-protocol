# Oracle Domain Reference — Project Blockchain

## System Architecture

```
[Off-chain data sources]
        ↓
[Oracle Nodes] — each a Docker container
    - Fetch data from N sources
    - Sign submission with node private key
    - Submit on-chain via OracleModule contract
        ↓
[OracleModule.sol]
    - Verifies node stake ≥ minimumStake
    - Stores submissions per round
    - Triggers aggregation after quorum
        ↓
[Go Aggregation Service]
    - Listens to SubmissionReceived events
    - Validates signatures off-chain
    - Computes medianized result
    - Writes aggregated value to PostgreSQL + Redis
    - Triggers slashing for outlier/missing nodes
```

## On-Chain Design (OracleModule.sol)

### Roles
- `ORACLE_NODE_ROLE`: registered oracle node, has staked minimum stake
- `ORACLE_MANAGER_ROLE`: can update parameters (min stake, round duration, outlier threshold)
- `SLASHER_ROLE`: backend-held key that executes slashing decisions

### Round Lifecycle
```
State: OPEN → QUORUM_MET → SETTLED → (next round OPEN)

OPEN: nodes submit data
QUORUM_MET: ≥ quorumThreshold nodes submitted; aggregation triggered
SETTLED: aggregated value finalized; round closed; next round begins
```

### Key Data Structures

```solidity
struct Round {
    uint256 roundId;
    uint256 startBlock;
    uint256 endBlock;
    uint256 aggregatedValue;    // scaled to 18 decimals
    uint256 submissionCount;
    RoundState state;
    bool settled;
}

struct Submission {
    uint256 roundId;
    address node;
    uint256 value;              // scaled to 18 decimals
    bytes signature;
    uint256 submittedAt;
}

struct NodeInfo {
    uint256 stakedAmount;
    uint256 slashedAmount;
    uint256 missedRounds;
    bool active;
}
```

### Events (Indexing Surface)

```solidity
event RoundStarted(uint256 indexed roundId, uint256 startBlock);
event SubmissionReceived(uint256 indexed roundId, address indexed node, uint256 value);
event RoundSettled(uint256 indexed roundId, uint256 aggregatedValue, uint256 submissionCount);
event NodeSlashed(address indexed node, uint256 amount, bytes32 reason);
event NodeStaked(address indexed node, uint256 amount);
event NodeUnstaked(address indexed node, uint256 amount);
```

## Aggregation Logic (Go Service)

### Medianization

```go
func Medianize(values []decimal.Decimal) decimal.Decimal {
    if len(values) == 0 {
        return decimal.Zero
    }
    sorted := make([]decimal.Decimal, len(values))
    copy(sorted, values)
    sort.Slice(sorted, func(i, j int) bool {
        return sorted[i].LessThan(sorted[j])
    })
    mid := len(sorted) / 2
    if len(sorted)%2 == 0 {
        return sorted[mid-1].Add(sorted[mid]).Div(decimal.NewFromInt(2))
    }
    return sorted[mid]
}
```

### Outlier Detection

Flag submissions deviating more than `outlierThresholdPct` from median:

```go
func DetectOutliers(submissions []Submission, median decimal.Decimal, thresholdPct decimal.Decimal) []address {
    var outliers []address
    for _, sub := range submissions {
        deviation := sub.Value.Sub(median).Abs().Div(median).Mul(decimal.NewFromInt(100))
        if deviation.GreaterThan(thresholdPct) {
            outliers = append(outliers, sub.Node)
        }
    }
    return outliers
}
```

### Quorum Validation

Before finalizing a round:
1. Count valid submissions (signature verified, node staked, submitted within window).
2. Require count ≥ `quorumThreshold` (e.g., 2/3 of registered nodes).
3. If quorum not met within `roundDuration` blocks: round fails, mark missed nodes.

## Slashing Logic

Slashing decisions are made off-chain (Go service) and executed on-chain via `slash(address node, uint256 amount, bytes32 reason)` called by `SLASHER_ROLE`.

### Slash Triggers

| Condition | Slash Amount | Reason Code |
|---|---|---|
| Outlier submission (deviation > threshold) | 1% of stake | `OUTLIER_SUBMISSION` |
| Missed round | 0.5% of stake | `MISSED_ROUND` |
| N consecutive misses | 10% of stake | `CONSECUTIVE_MISSES` |
| Invalid signature submitted | 5% of stake | `INVALID_SIGNATURE` |

### Slash Safety

- Slashing is batched per round (not per-node-per-event), executed after round settlement.
- Minimum stake floor enforced: if stake falls below `minStakeFloor`, node is auto-deactivated.
- Slashing decisions logged to PostgreSQL before execution (audit trail).
- `SLASHER_ROLE` key must be a hot wallet controlled by the backend, rotatable via governance.

## Fault Tolerance

- Oracle node failure: system continues if quorum is met by remaining nodes.
- RPC failure on node: node retries with exponential backoff (max 5 retries per round).
- Aggregation service crash: resumes from last settled round ID stored in PostgreSQL.
- Data source outage: nodes fetch from N ≥ 3 sources; use median of source values before signing.

## Signature Scheme

Nodes sign submissions using EIP-712 typed data:

```solidity
bytes32 constant SUBMISSION_TYPEHASH = keccak256(
    "Submission(uint256 roundId,uint256 value,address node,uint256 nonce)"
);
```

Aggregation service verifies signatures off-chain before including submissions in aggregation. On-chain contract verifies `ecrecover` on submission.

## Security Risks

| Risk | Mitigation |
|---|---|
| Sybil oracle nodes | Staking requirement + whitelist registry during v0.2 |
| Colluding majority | Medianization limits single-node manipulation; requires 50%+ collusion to shift median significantly |
| Flash loan to pass governance changing oracle params | Snapshot stake at round start, not current block |
| Griefing via spam submissions | Only staked nodes accepted; submission gas cost borne by node |
| SLASHER_ROLE key compromise | Rotate via governance timelock; slash amounts bounded per tx |
