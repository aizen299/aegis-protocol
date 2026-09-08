//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	minStake  = "10000000000000000000000" // 10,000e18
	feedName  = "ETH/USD"
	fundedWei = "0xDE0B6B3A7640000" // 1 ETH, for submission gas
)

type oracleDeployment struct {
	ChainID         int64  `json:"chainId"`
	DeployedAtBlock uint64 `json:"deployedAtBlock"`
	OracleRounds    string `json:"oracleRounds"`
	OracleStaking   string `json:"oracleStaking"`
	StakeToken      string `json:"stakeToken"`
}

// oracleNode is a signing identity. Keys are generated per run rather than fixed, so the test
// exercises real key material rather than a canned signature.
type oracleNode struct {
	key     evm.NodeKey
	keyHex  string
	address string
	id      types.Identity
}

func deployOracle(t *testing.T) oracleDeployment {
	t.Helper()
	root := repoRoot(t)

	cmd := exec.Command("forge", "script", "script/DeployOracleLocal.s.sol:DeployOracleLocal",
		"--rpc-url", anvilRPC, "--broadcast", "--silent")
	cmd.Dir = filepath.Join(root, "contracts")
	cmd.Env = append(os.Environ(), "PRIVATE_KEY="+deployerKey)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("oracle deploy failed: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(filepath.Join(root, "contracts", "deployments", "oracle-31337.json"))
	if err != nil {
		t.Fatalf("read oracle deployment: %v", err)
	}

	var d oracleDeployment
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse oracle deployment: %v", err)
	}
	d.OracleRounds = canonical(t, d.OracleRounds)
	d.OracleStaking = canonical(t, d.OracleStaking)
	d.StakeToken = canonical(t, d.StakeToken)
	return d
}

func newOracleNode(t *testing.T) oracleNode {
	t.Helper()

	key, err := evm.GenerateNodeKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return oracleNode{
		key:     key,
		keyHex:  key.Hex(),
		address: key.Address(),
		id:      key.Identity,
	}
}

// registerNodes funds each node with gas and stake, then registers it on-chain.
func registerNodes(t *testing.T, d oracleDeployment, count int) []oracleNode {
	t.Helper()

	nodes := make([]oracleNode, 0, count)
	for i := 0; i < count; i++ {
		node := newOracleNode(t)

		cast(t, "rpc", "--rpc-url", anvilRPC, "anvil_setBalance", node.address, fundedWei)
		send(t, deployerKey, d.StakeToken, "transfer(address,uint256)", node.address, minStake)
		send(t, node.keyHex, d.StakeToken, "approve(address,uint256)", d.OracleStaking, minStake)
		send(t, node.keyHex, d.OracleStaking, "register(uint256)", minStake)

		nodes = append(nodes, node)
	}
	return nodes
}

// submitSigned signs the submission in Go — the same path a node binary takes — and sends it.
// The contract verifies the signature, so this fails if the Go and Solidity EIP-712 encodings
// disagree in any respect: domain, typehash, field order, or the v offset.
func submitSigned(t *testing.T, d oracleDeployment, node oracleNode, roundID int64, value *big.Int) {
	t.Helper()

	roundsID, err := types.IdentityFromEVMHex(d.OracleRounds)
	if err != nil {
		t.Fatalf("rounds address: %v", err)
	}

	nonce := castCallUint(t, d.OracleRounds, "nonceOf(address)(uint256)", node.address)

	signature, err := evm.SignSubmission(node.key, chainID, roundsID, evm.OracleSubmission{
		RoundID: big.NewInt(roundID),
		FeedID:  evm.FeedID(feedName),
		Value:   value,
		Node:    node.id,
		Nonce:   nonce,
	})
	if err != nil {
		t.Fatalf("sign submission: %v", err)
	}

	send(t, node.keyHex, d.OracleRounds, "submit(uint256,uint256,uint256,bytes)",
		fmt.Sprint(roundID), value.String(), nonce.String(), "0x"+fmt.Sprintf("%x", signature))
}

func castCallUint(t *testing.T, to, sig string, args ...string) *big.Int {
	t.Helper()
	return parseUintLine(t, call(t, to, sig, args...))
}

// cast annotates large numbers ("3000000000000000000000 [3e21]") and prints one return value per
// line, so take the first token of the first line and drop the annotation.
func parseUintLine(t *testing.T, out string) *big.Int {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(out), "\n")
	return parseUintToken(t, lines[0])
}

func parseUintToken(t *testing.T, line string) *big.Int {
	t.Helper()

	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		t.Fatalf("expected a number, got %q", line)
	}

	n, ok := new(big.Int).SetString(fields[0], 10)
	if !ok {
		t.Fatalf("parse %q as uint", fields[0])
	}
	return n
}

func feedIDHex() string {
	id := evm.FeedID(feedName)
	return "0x" + fmt.Sprintf("%x", id[:])
}

func fmt64(n int64) string { return fmt.Sprint(n) }

func readFeed(t *testing.T, d oracleDeployment, maxStaleness string) (value, settledAt, roundID *big.Int) {
	t.Helper()

	out := call(t, d.OracleRounds, "getValue(bytes32,uint256)(uint256,uint256,uint256)",
		feedIDHex(), maxStaleness)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		t.Fatalf("getValue returned %q, want three values", out)
	}
	return parseUintToken(t, lines[0]), parseUintToken(t, lines[1]), parseUintToken(t, lines[2])
}

// castErr runs cast without failing the test, for the paths that are expected to revert.
func castErr(t *testing.T, args ...string) (string, error) {
	t.Helper()

	out, err := exec.Command("cast", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func truncateOracle(t *testing.T, dsn string) {
	t.Helper()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect for truncate: %v", err)
	}
	defer conn.Close(ctx)

	const q = `TRUNCATE oracle_slashings, oracle_submissions, oracle_rounds, oracle_nodes,
	           oracle_feeds, vaults, assets, indexer_cursors RESTART IDENTITY CASCADE`
	if _, err := conn.Exec(ctx, q); err != nil {
		t.Fatalf("truncate oracle tables: %v", err)
	}
}

// reasonHex renders a slash reason as the right-padded bytes32 the contract expects.
func reasonHex(reason string) string {
	var padded [32]byte
	copy(padded[:], reason)
	return "0x" + fmt.Sprintf("%x", padded[:])
}

func queryString(t *testing.T, s *oracleStack, sql string, args ...any) string {
	t.Helper()
	var out string
	if err := s.store.QueryRowForTest(context.Background(), sql, args...).Scan(&out); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return out
}

func queryInt(t *testing.T, s *oracleStack, sql string, args ...any) int64 {
	t.Helper()
	var out int64
	if err := s.store.QueryRowForTest(context.Background(), sql, args...).Scan(&out); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return out
}
