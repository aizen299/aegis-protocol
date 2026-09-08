//go:build e2e

// Package e2e exercises the seam every other test fakes: a real contract emitting a real log,
// decoded by the real EVM client, indexed by the real indexer, into a real Postgres, served by the
// real API. Every component is unit-tested against a mock of its neighbour; this is the only place
// they meet.
//
// Run with `make e2e`, which brings up Anvil and Postgres first.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	anvilRPC = "http://127.0.0.1:8545"
	chainID  = int64(31337)

	// Anvil's deterministic accounts. Public knowledge, worthless off a local chain.
	deployerKey  = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	deployerAddr = "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"
	aliceKey     = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	aliceAddr    = "0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
)

type deployment struct {
	Asset           string `json:"asset"`
	ChainID         int64  `json:"chainId"`
	DeployedAtBlock uint64 `json:"deployedAtBlock"`
	VaultProxy      string `json:"vaultEngineProxy"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return abs
}

// deployFresh redeploys the vault and a six-decimal test token. Six is deliberate: eighteen is the
// value every layer would get right by accident.
func deployFresh(t *testing.T) deployment {
	t.Helper()
	root := repoRoot(t)

	cmd := exec.Command("forge", "script", "script/DeployLocal.s.sol:DeployLocal",
		"--rpc-url", anvilRPC, "--broadcast", "--silent")
	cmd.Dir = filepath.Join(root, "contracts")
	cmd.Env = append(os.Environ(), "PRIVATE_KEY="+deployerKey)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(filepath.Join(root, "contracts", "deployments", "31337.json"))
	if err != nil {
		t.Fatalf("read deployment artifact: %v", err)
	}

	var d deployment
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse deployment artifact: %v", err)
	}
	if d.VaultProxy == "" || d.Asset == "" {
		t.Fatalf("deployment artifact is incomplete: %s", raw)
	}

	// forge writes EIP-55 checksummed addresses; the protocol's canonical stored form is lowercase.
	// Normalising here is what every consumer of an external address must do.
	d.VaultProxy = canonical(t, d.VaultProxy)
	d.Asset = canonical(t, d.Asset)
	return d
}

func canonical(t *testing.T, address string) string {
	t.Helper()

	id, err := types.IdentityFromEVMHex(address)
	if err != nil {
		t.Fatalf("canonicalise %s: %v", address, err)
	}
	return id.EVMHex()
}

func cast(t *testing.T, args ...string) string {
	t.Helper()

	out, err := exec.Command("cast", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("cast %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func send(t *testing.T, key, to, sig string, args ...string) {
	t.Helper()

	full := append([]string{"send", "--rpc-url", anvilRPC, "--private-key", key, to, sig}, args...)
	cast(t, full...)
}

func call(t *testing.T, to, sig string, args ...string) string {
	t.Helper()

	full := append([]string{"call", "--rpc-url", anvilRPC, to, sig}, args...)
	return cast(t, full...)
}

func headBlock(t *testing.T) uint64 {
	t.Helper()

	var head uint64
	if _, err := fmt.Sscan(cast(t, "block-number", "--rpc-url", anvilRPC), &head); err != nil {
		t.Fatalf("parse head: %v", err)
	}
	return head
}

// mineBlocks advances the chain so events fall below the indexer's confirmation window.
func mineBlocks(t *testing.T, n int) {
	t.Helper()
	cast(t, "rpc", "--rpc-url", anvilRPC, "anvil_mine", fmt.Sprintf("0x%x", n))
}

func requireDeps(t *testing.T) {
	t.Helper()

	for _, bin := range []string{"forge", "cast"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not on PATH; run `make e2e`", bin)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := exec.CommandContext(ctx, "cast", "chain-id", "--rpc-url", anvilRPC).Run(); err != nil {
		t.Skipf("anvil is not reachable at %s; run `make e2e`", anvilRPC)
	}
}

// truncate clears indexed state between runs. The E2E redeploys contracts each time, so stale rows
// from a previous run would otherwise be indistinguishable from this run's.
func truncate(t *testing.T, dsn string) {
	t.Helper()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect for truncate: %v", err)
	}
	defer conn.Close(ctx)

	const q = `TRUNCATE vault_deposits, vault_withdrawals, assets, indexer_cursors RESTART IDENTITY CASCADE`
	if _, err := conn.Exec(ctx, q); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
