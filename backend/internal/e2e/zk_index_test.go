//go:build e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/api"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	zksvc "github.com/aizen299/aegis-protocol/backend/internal/zk"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	zkcontracts "github.com/aizen299/aegis-protocol/backend/pkg/contracts/zk"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// The zk handlers were written against fakes whose payloads this file did not produce. A leaf index
// and a bytes32 commitment normalise differently coming off a real log than they do in a hand-built
// map, and the tree the indexer mirrors has to match the one the contract actually built.
type zkStack struct {
	deployment zkDeployment
	store      *db.Store
	client     *evm.Client
	indexer    *indexer.Indexer
}

func setupZkStack(t *testing.T, d zkDeployment) *zkStack {
	t.Helper()
	ctx := context.Background()

	cfg := &config.Config{}
	cfg.DB.DSN = envOr("DB_DSN", "postgres://pb:pb_local@localhost:5432/aegis?sslmode=disable")
	cfg.DB.MaxOpenConns = 8
	cfg.DB.MinConns = 1

	store, err := db.New(ctx, cfg)
	if err != nil {
		t.Skipf("postgres unavailable: %v (run `make e2e`)", err)
	}
	t.Cleanup(store.Close)
	truncateZk(t, cfg.DB.DSN)

	treeID, err := types.IdentityFromEVMHex(d.CommitmentTree)
	if err != nil {
		t.Fatalf("tree address: %v", err)
	}
	gateID, err := types.IdentityFromEVMHex(d.ZkVaultGate)
	if err != nil {
		t.Fatalf("gate address: %v", err)
	}

	client, err := evm.New(ctx, evm.Options{
		RPCURL:            anvilRPC,
		ChainID:           chainID,
		ConfirmationDepth: 1,
		Contracts: []evm.Registration{
			{Address: treeID, ABI: zkcontracts.TreeABI()},
			{Address: gateID, ABI: zkcontracts.GateABI()},
		},
	})
	if err != nil {
		t.Fatalf("evm client: %v", err)
	}
	t.Cleanup(client.Close)

	log := zerolog.New(io.Discard)
	idx := indexer.New(client, store, log, indexer.Options{
		ServiceName: "e2e-zk",
		StartBlock:  d.DeployedAtBlock,
		BatchSize:   500,
	},
		indexer.NewZkHandler(store, client, evm.NewZkReader(client), treeID, gateID),
	)
	if err := idx.Restore(ctx); err != nil {
		t.Fatalf("restore cursor: %v", err)
	}

	return &zkStack{deployment: d, store: store, client: client, indexer: idx}
}

func (s *zkStack) indexToHead(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	mineBlocks(t, 2)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("zk indexer stalled")
		}
		advanced, err := s.indexer.Step(ctx)
		if err != nil {
			t.Fatalf("zk indexer step: %v", err)
		}
		if !advanced {
			return
		}
	}
}

func (s *zkStack) apiHandler(t *testing.T) http.Handler {
	t.Helper()

	cfg := &config.Config{}
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 30 * time.Second
	cfg.Chain.ChainID = chainID

	log := zerolog.New(io.Discard)
	srv := api.NewServer(cfg, log, api.Deps{
		Store:   s.store,
		Zk:      zksvc.NewService(s.store, nil, log, chainID),
		Chain:   s.client,
		ChainID: chainID,
	})
	return srv.Handler()
}

func truncateZk(t *testing.T, dsn string) {
	t.Helper()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect for truncate: %v", err)
	}
	defer conn.Close(ctx)

	const q = `TRUNCATE zk_private_actions, zk_actions, zk_commitments, zk_gates,
	           indexer_cursors RESTART IDENTITY CASCADE`
	if _, err := conn.Exec(ctx, q); err != nil {
		t.Fatalf("truncate zk tables: %v", err)
	}
}

func zkQueryInt(t *testing.T, s *zkStack, sql string, args ...any) int64 {
	t.Helper()
	var out int64
	if err := s.store.QueryRowForTest(context.Background(), sql, args...).Scan(&out); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return out
}

func zkQueryString(t *testing.T, s *zkStack, sql string, args ...any) string {
	t.Helper()
	var out string
	if err := s.store.QueryRowForTest(context.Background(), sql, args...).Scan(&out); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return out
}

// insertCommitment puts one commitment in the tree and returns it.
func insertCommitment(t *testing.T, d zkDeployment, secret string) string {
	t.Helper()
	value := strings.Fields(call(t, d.PoseidonT2, "poseidon(uint256[1])(uint256)", "["+secret+"]"))[0]
	commitment := toBytes32(t, value)
	send(t, deployerKey, d.CommitmentTree, "insert(bytes32)", commitment)
	return commitment
}

// The whole path out of real logs: the tree mirrored leaf by leaf, and a spent nullifier recorded
// against the gate it was spent at.
func TestZkLifecycleIsIndexedFromRealLogs(t *testing.T) {
	requireDeps(t)
	d := deployZk(t)
	s := setupZkStack(t, d)

	insertCommitment(t, d, zkSecret)
	proof, inputs := prove(t, d.ZkVaultGate, chainID, zkActionID)
	root, nullifier := inputs[0], inputs[1]

	send(t, deployerKey, d.ZkVaultGate, "registerAction(bytes32,string)",
		toBytes32(t, zkActionID), "vault-membership")
	send(t, deployerKey, d.ZkVaultGate, "executePrivateAction(bytes,bytes32,bytes32,bytes32)",
		proof, root, nullifier, toBytes32(t, zkActionID))

	s.indexToHead(t)

	leaves := zkQueryInt(t, s,
		`SELECT count(*) FROM zk_commitments WHERE chain_id = $1 AND tree_address = $2`,
		chainID, d.CommitmentTree)
	if leaves != 1 {
		t.Fatalf("commitments = %d, want 1", leaves)
	}

	// The mirror stores the root each insert produced, so it can be checked against the chain leaf
	// by leaf rather than only at the tip.
	indexedRoot := zkQueryString(t, s,
		`SELECT root_after FROM zk_commitments WHERE chain_id = $1 AND leaf_index = 0`, chainID)
	if !strings.EqualFold(indexedRoot, root) {
		t.Fatalf("mirrored root %s, chain proved against %s", indexedRoot, root)
	}

	spends := zkQueryInt(t, s,
		`SELECT count(*) FROM zk_private_actions WHERE chain_id = $1 AND nullifier = $2`,
		chainID, strings.ToLower(nullifier))
	if spends != 1 {
		t.Fatalf("private actions = %d, want 1", spends)
	}

	// The gate's wiring is discovered from chain, not configured.
	tree := zkQueryString(t, s,
		`SELECT tree_address FROM zk_gates WHERE chain_id = $1 AND address = $2`,
		chainID, d.ZkVaultGate)
	if tree != d.CommitmentTree {
		t.Errorf("gate's tree = %s, want %s", tree, d.CommitmentTree)
	}
}

// The API over real indexed rows, including the caveat the plan commits to surfacing.
func TestZkAPIServesIndexedRows(t *testing.T) {
	requireDeps(t)
	d := deployZk(t)
	s := setupZkStack(t, d)

	insertCommitment(t, d, zkSecret)
	proof, inputs := prove(t, d.ZkVaultGate, chainID, zkActionID)
	send(t, deployerKey, d.ZkVaultGate, "registerAction(bytes32,string)",
		toBytes32(t, zkActionID), "vault-membership")
	send(t, deployerKey, d.ZkVaultGate, "executePrivateAction(bytes,bytes32,bytes32,bytes32)",
		proof, inputs[0], inputs[1], toBytes32(t, zkActionID))

	s.indexToHead(t)
	srv := s.apiHandler(t)

	status, body := apiGet(t, srv, "/v1/zk/gate")
	if status != http.StatusOK {
		t.Fatalf("gate: status %d, body %v", status, body)
	}
	if got := body["tree"]; got != d.CommitmentTree {
		t.Errorf("tree = %v, want %s", got, d.CommitmentTree)
	}

	// A membership proof is only as private as the number of members.
	status, body = apiGet(t, srv, "/v1/zk/anonymity-set")
	if status != http.StatusOK {
		t.Fatalf("anonymity set: status %d", status)
	}
	if got := body["leafCount"]; got != float64(1) {
		t.Errorf("leafCount = %v, want 1", got)
	}

	status, body = apiGet(t, srv, "/v1/zk/nullifiers/"+strings.ToLower(inputs[1]))
	if status != http.StatusOK {
		t.Fatalf("nullifier: status %d, body %v", status, body)
	}
	if body["spent"] != true {
		t.Errorf("spent = %v, want true — the chain accepted this nullifier", body["spent"])
	}

	status, body = apiGet(t, srv, "/v1/zk/commitments")
	if status != http.StatusOK {
		t.Fatalf("commitments: status %d", status)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("commitment items = %#v", body["items"])
	}
	if items[0].(map[string]any)["leafIndex"] != float64(0) {
		t.Errorf("leafIndex = %v", items[0].(map[string]any)["leafIndex"])
	}

	status, body = apiGet(t, srv, "/v1/zk/actions")
	if status != http.StatusOK {
		t.Fatalf("actions: status %d", status)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 1 {
		t.Fatalf("action items = %#v", body["items"])
	}
}

// The privacy property, checked against what the API actually returns rather than what the types
// declare. A sender on both tables would make correlating a commitment with its spend a join.
func TestServedZkRowsCarryNoSender(t *testing.T) {
	requireDeps(t)
	d := deployZk(t)
	s := setupZkStack(t, d)

	insertCommitment(t, d, zkSecret)
	proof, inputs := prove(t, d.ZkVaultGate, chainID, zkActionID)
	send(t, deployerKey, d.ZkVaultGate, "registerAction(bytes32,string)",
		toBytes32(t, zkActionID), "vault-membership")
	send(t, deployerKey, d.ZkVaultGate, "executePrivateAction(bytes,bytes32,bytes32,bytes32)",
		proof, inputs[0], inputs[1], toBytes32(t, zkActionID))

	s.indexToHead(t)
	srv := s.apiHandler(t)

	// The deployer sent both transactions, so if anything leaked a sender it would be this address.
	for _, path := range []string{"/v1/zk/commitments", "/v1/zk/private-actions"} {
		_, body := apiGet(t, srv, path)
		items, ok := body["items"].([]any)
		if !ok || len(items) == 0 {
			t.Fatalf("%s: items = %#v", path, body["items"])
		}

		row := items[0].(map[string]any)
		for key, value := range row {
			if text, isString := value.(string); isString {
				if strings.EqualFold(text, deployerAddr) {
					t.Errorf("%s exposes the sender through %q", path, key)
				}
			}
		}
		for _, forbidden := range []string{"sender", "from", "user", "owner", "account"} {
			if _, present := row[forbidden]; present {
				t.Errorf("%s exposes %q", path, forbidden)
			}
		}
	}
}

// Reprocessing the same logs must not duplicate a leaf or a spend. The tree's leaf index is stable,
// so a duplicate would also corrupt every path built afterwards.
func TestZkReindexingIsIdempotent(t *testing.T) {
	requireDeps(t)
	d := deployZk(t)
	s := setupZkStack(t, d)

	insertCommitment(t, d, zkSecret)
	proof, inputs := prove(t, d.ZkVaultGate, chainID, zkActionID)
	send(t, deployerKey, d.ZkVaultGate, "registerAction(bytes32,string)",
		toBytes32(t, zkActionID), "vault-membership")
	send(t, deployerKey, d.ZkVaultGate, "executePrivateAction(bytes,bytes32,bytes32,bytes32)",
		proof, inputs[0], inputs[1], toBytes32(t, zkActionID))

	s.indexToHead(t)

	head := s.indexer.Cursor()
	if head <= d.DeployedAtBlock {
		t.Fatalf("cursor never advanced past deployment (%d); there is nothing to replay", head)
	}

	if err := s.store.RewindCursorForTest(context.Background(), "e2e-zk", chainID, d.DeployedAtBlock); err != nil {
		t.Fatalf("rewind cursor: %v", err)
	}
	if err := s.indexer.Restore(context.Background()); err != nil {
		t.Fatalf("restore cursor: %v", err)
	}
	if got := s.indexer.Cursor(); got != d.DeployedAtBlock {
		t.Fatalf("cursor = %d after the rewind, want %d — the replay never happened", got, d.DeployedAtBlock)
	}

	s.indexToHead(t)

	leaves := zkQueryInt(t, s, `SELECT count(*) FROM zk_commitments WHERE chain_id = $1`, chainID)
	if leaves != 1 {
		t.Fatalf("commitments = %d after a replay, want 1", leaves)
	}
	spends := zkQueryInt(t, s, `SELECT count(*) FROM zk_private_actions WHERE chain_id = $1`, chainID)
	if spends != 1 {
		t.Fatalf("private actions = %d after a replay, want 1", spends)
	}
}

// The anonymity set is the thing the plan records as unresolved, so it has to track the tree.
func TestTheAnonymitySetTracksTheTree(t *testing.T) {
	requireDeps(t)
	d := deployZk(t)
	s := setupZkStack(t, d)

	// Registering an action gives the gate a row, so /v1/zk/gate resolves before any commitment.
	send(t, deployerKey, d.ZkVaultGate, "registerAction(bytes32,string)",
		toBytes32(t, zkActionID), "vault-membership")
	s.indexToHead(t)

	srv := s.apiHandler(t)
	_, body := apiGet(t, srv, "/v1/zk/anonymity-set")
	if got := body["leafCount"]; got != float64(0) {
		t.Fatalf("leafCount = %v on an empty tree, want 0", got)
	}

	for _, secret := range []string{"424242", "525252", "626262"} {
		insertCommitment(t, d, secret)
	}
	s.indexToHead(t)

	_, body = apiGet(t, srv, "/v1/zk/anonymity-set")
	if got := body["leafCount"]; got != float64(3) {
		t.Fatalf("leafCount = %v, want 3", got)
	}
	if body["currentRoot"] == nil || body["currentRoot"] == "" {
		t.Error("the anonymity set reports no root")
	}
}
