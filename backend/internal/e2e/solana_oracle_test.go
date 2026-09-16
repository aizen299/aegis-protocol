//go:build e2e

package e2e

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math/big"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/api"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/svm"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	oraclesvc "github.com/aizen299/aegis-protocol/backend/internal/oracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/soloracle"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	solanaMinStake      = uint64(1_000_000)
	solanaRoundDuration = int64(120)
)

type solanaOracle struct {
	program   pbtypes.Identity
	authority solanaKey
	mint      pbtypes.Identity
	feed      [32]byte
}

func (o solanaOracle) pda(seeds ...[]byte) pbtypes.Identity {
	id, _, _ := svm.FindProgramAddress(seeds, o.program)
	return id
}

func (o solanaOracle) events() []accountMeta {
	return []accountMeta{{Key: o.pda([]byte("__event_authority"))}, {Key: o.program}}
}

func i64le(v int64) []byte { return binary.LittleEndian.AppendUint64(nil, uint64(v)) }

// setupSolanaOracle initializes the oracle with the test key in every role, and registers ETH/USD.
func setupSolanaOracle(t *testing.T, program pbtypes.Identity, authority solanaKey) solanaOracle {
	t.Helper()
	airdrop(t, authority.Identity(), 100_000_000_000)
	mint := newSolanaKey(t)
	sendOK(t, authority, []solanaKey{mint}, "confirmed",
		createAccount(t, authority, mint, 82, svm.TokenProgram),
		initializeMint(mint.Identity(), authority.Identity(), 6),
	)
	o := solanaOracle{program: program, authority: authority, mint: mint.Identity(), feed: svm.FeedID("ETH/USD")}
	me := authority.Identity()

	data := anchorDiscriminator("initialize")
	data = append(data, i64le(solanaChainID)...)
	for i := 0; i < 3; i++ {
		data = append(data, me[:]...)
	}
	data = append(data, u64le(solanaMinStake)...)
	data = append(data, u64le(solanaMinStake*9/10)...)
	data = append(data, i64le(86_400)...)
	data = append(data, u64le(1_000)...)
	data = append(data, 4)
	data = append(data, i64le(solanaRoundDuration)...)
	data = append(data, u64le(6_000)...)
	data = append(data, 2)
	sendOK(t, authority, nil, "confirmed", instruction{Program: program, Data: data, Accounts: []accountMeta{
		{Key: me, Signer: true, Writable: true},
		{Key: o.pda([]byte("config")), Writable: true},
		{Key: o.pda([]byte("stake_vault")), Writable: true},
		{Key: o.mint},
		{Key: program},
		{Key: pda(t, bpfLoaderUpgrade, program[:])},
		{Key: svm.TokenProgram},
		{Key: systemProgram},
	}})

	name := "ETH/USD"
	data = anchorDiscriminator("register_feed")
	data = append(data, o.feed[:]...)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(name)))
	data = append(data, name...)
	data = append(data, 8)
	sendOK(t, authority, nil, "confirmed", instruction{Program: program, Data: data, Accounts: append([]accountMeta{
		{Key: me, Signer: true, Writable: true},
		{Key: o.pda([]byte("config"))},
		{Key: o.pda([]byte("feed"), o.feed[:]), Writable: true},
		{Key: systemProgram},
	}, o.events()...)})
	return o
}

func (o solanaOracle) registerNode(t *testing.T) solanaKey {
	t.Helper()
	node, tokens := newSolanaKey(t), newSolanaKey(t)
	airdrop(t, node.Identity(), 10_000_000_000)
	sendOK(t, o.authority, []solanaKey{tokens}, "confirmed",
		createAccount(t, o.authority, tokens, 165, svm.TokenProgram),
		initializeTokenAccount(tokens.Identity(), o.mint, node.Identity()),
		mintTo(o.mint, tokens.Identity(), o.authority, solanaMinStake),
	)
	id := node.Identity()
	sendOK(t, node, nil, "confirmed", instruction{
		Program: o.program,
		Data:    append(anchorDiscriminator("register"), u64le(solanaMinStake)...),
		Accounts: append([]accountMeta{
			{Key: id, Signer: true, Writable: true},
			{Key: o.pda([]byte("config")), Writable: true},
			{Key: o.pda([]byte("node"), id[:]), Writable: true},
			{Key: tokens.Identity(), Writable: true},
			{Key: o.pda([]byte("stake_vault")), Writable: true},
			{Key: svm.TokenProgram},
			{Key: systemProgram},
		}, o.events()...),
	})
	return node
}

func (o solanaOracle) openRound(t *testing.T, current, next uint64) {
	t.Helper()
	me := o.authority.Identity()
	sendOK(t, o.authority, nil, "confirmed", instruction{
		Program: o.program,
		Data:    anchorDiscriminator("open_round"),
		Accounts: append([]accountMeta{
			{Key: me, Signer: true, Writable: true},
			{Key: o.pda([]byte("config")), Writable: true},
			{Key: o.pda([]byte("feed"), o.feed[:]), Writable: true},
			{Key: o.pda([]byte("round"), u64le(current))},
			{Key: o.pda([]byte("round"), u64le(next)), Writable: true},
			{Key: systemProgram},
		}, o.events()...),
	})
}

func (o solanaOracle) settle(t *testing.T, round uint64) string {
	t.Helper()
	return sendOK(t, o.authority, nil, "finalized", instruction{
		Program: o.program,
		Data:    anchorDiscriminator("settle_round"),
		Accounts: append([]accountMeta{
			{Key: o.pda([]byte("config"))},
			{Key: o.pda([]byte("round"), u64le(round)), Writable: true},
			{Key: o.pda([]byte("feed"), o.feed[:]), Writable: true},
		}, o.events()...),
	})
}

// §12.8, steps 4b and 4c: nodes submit through the production Go adapter, the program verifies their
// ed25519 attestations, the rounds and node set are indexed and served under the Solana chain, and the
// aggregator verifies the attestations off chain and slashes an outlier and a missed round.
func TestASolanaOracleRoundIsSubmittedFromGoAndIndexed(t *testing.T) {
	s := setupStack(t)
	authority := requireSolana(t)
	truncateOracle(t, s.cfg.DB.DSN)
	ctx := context.Background()

	idl, err := svm.ParseIDL(soloracle.IDL)
	if err != nil {
		t.Fatal(err)
	}
	o := setupSolanaOracle(t, idl.Program, authority)
	nodes := []solanaKey{o.registerNode(t), o.registerNode(t), o.registerNode(t)}
	// Registered but never submits. Round 1 settles early, which excuses it; round 2 runs to its
	// deadline, which does not.
	silent := o.registerNode(t)
	o.openRound(t, 0, 1)

	client, err := svm.New(ctx, svm.Options{RPCURL: solanaRPC, ChainID: solanaChainID, Programs: []svm.Registration{{IDL: idl}}})
	if err != nil {
		t.Fatal(err)
	}
	feedHex := "0x" + hex.EncodeToString(o.feed[:])
	// Within the outlier threshold of each other, so round 1 decides no penalty.
	values := []int64{202_000_000_000, 200_000_000_000, 201_000_000_000}

	for i, key := range nodes {
		chain, err := svm.NewOracleNodeChain(client, key)
		if err != nil {
			t.Fatal(err)
		}
		view, err := chain.CurrentRound(ctx, feedHex)
		if err != nil {
			t.Fatal(err)
		}
		if !view.Open || !view.Eligible || view.Submitted || view.Decimals != 8 || view.RoundID.String() != "1" {
			t.Fatalf("node %d sees %+v", i, view)
		}
		if _, err := chain.Submit(ctx, view.RoundID, feedHex, pbtypes.NewRaw(big.NewInt(values[i]))); err != nil {
			t.Fatalf("node %d submit: %v", i, err)
		}
		if view, _ = chain.CurrentRound(ctx, feedHex); !view.Submitted {
			t.Fatalf("node %d: its submission is not visible", i)
		}
		// A second submission is refused in simulation, before it costs a fee.
		if _, err := chain.Submit(ctx, view.RoundID, feedHex, pbtypes.NewRaw(big.NewInt(values[i]))); err == nil {
			t.Fatalf("node %d submitted twice", i)
		}
	}

	settleSig := o.settle(t, 1)
	lastSlot := transactionSlot(t, settleSig)

	handlerClient, err := svm.New(ctx, svm.Options{RPCURL: solanaRPC, ChainID: solanaChainID, Programs: []svm.Registration{{IDL: idl}}})
	if err != nil {
		t.Fatal(err)
	}
	idx := indexer.New(handlerClient, s.store, zerolog.New(io.Discard), indexer.Options{
		ServiceName: "e2e-solana-oracle",
		BatchSize:   1_000_000,
	},
		indexer.NewOracleRoundsHandler(s.store, handlerClient, o.program),
		indexer.NewOracleStakingHandler(s.store, handlerClient, o.program, o.mint))
	if err := idx.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for idx.Cursor() < lastSlot {
		if time.Now().After(deadline) {
			t.Fatalf("indexer stalled at slot %d, want %d", idx.Cursor(), lastSlot)
		}
		if _, err := idx.Step(ctx); err != nil {
			t.Fatalf("indexer step: %v", err)
		}
	}

	round, err := s.store.OracleRound(ctx, solanaChainID, pbtypes.NewRaw(big.NewInt(1)))
	if err != nil {
		t.Fatal(err)
	}
	if round.State != pbtypes.RoundStateSettled || round.AggregatedValue.String() != "201000000000" || round.SubmissionCount != 3 || round.EligibleCount != 4 {
		t.Fatalf("round = %+v", round)
	}
	if round.FeedID != feedHex || round.Decimals != 8 || round.FeedName != "ETH/USD" {
		t.Errorf("feed = %s %d %q", round.FeedID, round.Decimals, round.FeedName)
	}

	submissions, err := s.store.ListOracleSubmissions(ctx, solanaChainID, round.RoundID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(submissions) != 3 {
		t.Fatalf("indexed %d submissions, want 3", len(submissions))
	}
	sort.Slice(submissions, func(i, j int) bool { return submissions[i].LogIndex < submissions[j].LogIndex })
	for _, sub := range submissions {
		node, err := svm.Decode(sub.Node)
		if err != nil {
			t.Fatal(err)
		}
		signature, err := s.store.SubmissionSignature(ctx, solanaChainID, round.RoundID, sub.Node)
		if err != nil {
			t.Fatal(err)
		}
		// The stored attestation must verify off chain over the bytes the program checked. This is
		// what the aggregator will rely on in 4c.
		message, err := svm.SubmissionMessage(o.program, solanaChainID, 1, o.feed, sub.Value.Big(), node, sub.Nonce.Big().Uint64())
		if err != nil {
			t.Fatal(err)
		}
		if !sub.NonceKnown || len(signature) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(node[:]), message, signature) {
			t.Errorf("submission from %s: nonce known %v, %d-byte signature does not verify", sub.Node, sub.NonceKnown, len(signature))
		}
	}

	for _, key := range append(nodes, silent) {
		node, err := s.store.OracleNode(ctx, solanaChainID, svm.Encode(key.Identity()))
		if err != nil {
			t.Fatal(err)
		}
		if !node.Active || node.StakedAmount.String() != "1000000" || node.Decimals != 6 || node.StakeAsset != svm.Encode(o.mint) {
			t.Errorf("node = %+v", node)
		}
	}
	intervals, err := s.store.NodeIntervalsAt(ctx, solanaChainID, round.NodeSetVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(intervals) != 4 {
		t.Errorf("%d nodes were in the set the round froze, want 4", len(intervals))
	}

	srv := api.NewServer(s.cfg, zerolog.New(io.Discard), api.Deps{
		Store: s.store,
		Cache: s.cache,
		Chains: []api.ChainDeps{
			{ID: chainID, Codec: s.client},
			{ID: solanaChainID, Codec: svm.Codec{}, Oracle: oraclesvc.NewService(s.store, s.cache, zerolog.New(io.Discard), solanaChainID)},
		},
	}).Handler()
	status, body := serve(t, srv, "/v1/oracle/rounds/1?chain=solana-localnet")
	if status != http.StatusOK || body["aggregatedValue"] != "201000000000" || body["state"] != "settled" {
		t.Errorf("round over the API: status %d, body %v", status, body)
	}
	status, body = serve(t, srv, "/v1/oracle/nodes/"+svm.Encode(nodes[0].Identity())+"?chain=solana-localnet")
	if status != http.StatusOK || body["stakedAmount"] != "1000000" {
		t.Errorf("node over the API: status %d, body %v", status, body)
	}

	solanaSlashingRound(t, s, o, idx, client, nodes, silent)
}

// solanaSlashingRound is step 4c: a round that runs to its deadline with one outlier and one silent
// node, judged and executed by the real aggregator and executor with the Solana verifier and slasher.
func solanaSlashingRound(t *testing.T, s *stack, o solanaOracle, idx *indexer.Indexer, client *svm.Client, nodes []solanaKey, silent solanaKey) {
	t.Helper()
	ctx := context.Background()
	me := o.authority.Identity()

	// Short enough to wait out, long enough for three submissions to confirm.
	sendOK(t, o.authority, nil, "confirmed", instruction{
		Program: o.program,
		Data:    append(anchorDiscriminator("set_round_duration"), i64le(25)...),
		Accounts: append([]accountMeta{
			{Key: me, Signer: true},
			{Key: o.pda([]byte("config")), Writable: true},
		}, o.events()...),
	})
	o.openRound(t, 1, 2)

	feedHex := "0x" + hex.EncodeToString(o.feed[:])
	values := []int64{100_000_000_000, 101_000_000_000, 150_000_000_000}
	var deadline time.Time
	for i, key := range nodes {
		chain, err := svm.NewOracleNodeChain(client, key)
		if err != nil {
			t.Fatal(err)
		}
		view, err := chain.CurrentRound(ctx, feedHex)
		if err != nil || !view.Open || !view.Eligible {
			t.Fatalf("node %d sees %+v, err %v", i, view, err)
		}
		deadline = view.Deadline
		if _, err := chain.Submit(ctx, view.RoundID, feedHex, pbtypes.NewRaw(big.NewInt(values[i]))); err != nil {
			t.Fatalf("node %d submit: %v", i, err)
		}
	}

	// Settle only once the chain's clock is past the deadline: a round settled early excuses the
	// silent node, and this round is meant to count its silence.
	for waited := time.Now(); ; time.Sleep(time.Second) {
		var slot uint64
		if err := solanaCall(ctx, "getSlot", []any{map[string]any{"commitment": "confirmed"}}, &slot); err != nil {
			t.Fatal(err)
		}
		var blockTime int64
		if err := solanaCall(ctx, "getBlockTime", []any{slot}, &blockTime); err == nil && blockTime > deadline.Unix() {
			break
		}
		if time.Since(waited) > 90*time.Second {
			t.Fatal("the validator clock never passed the round deadline")
		}
	}
	settled := o.settle(t, 2)
	indexThrough(t, idx, transactionSlot(t, settled))

	slashKey := o.authority
	slasher, err := svm.NewSlasher(client, o.program, slashKey)
	if err != nil {
		t.Fatal(err)
	}
	log := zerolog.New(io.Discard)
	aggregator := oraclesvc.NewAggregator(s.store, svm.NewSubmissionVerifier(o.program), log, oraclesvc.AggregatorOptions{ChainID: solanaChainID})
	executor := oraclesvc.NewExecutor(s.store, slasher, log, oraclesvc.ExecutorOptions{ChainID: solanaChainID, MaxAttempts: 3})

	for {
		n, err := aggregator.Step(ctx)
		if err != nil {
			t.Fatalf("aggregate: %v", err)
		}
		if n == 0 {
			break
		}
	}
	for {
		n, err := aggregator.JudgeMisses(ctx)
		if err != nil {
			t.Fatalf("judge misses: %v", err)
		}
		if n == 0 {
			break
		}
	}

	count := func(sql string, args ...any) int64 {
		t.Helper()
		var out int64
		if err := s.store.QueryRowForTest(ctx, sql, args...).Scan(&out); err != nil {
			t.Fatalf("query %q: %v", sql, err)
		}
		return out
	}
	invalid := count(`SELECT count(*) FROM oracle_submissions WHERE chain_id = $1 AND signature_valid = false`, solanaChainID)
	if invalid != 0 {
		t.Errorf("%d genuine Solana attestations failed off-chain verification", invalid)
	}
	decided := func(node solanaKey, reason string) int64 {
		return count(`SELECT count(*) FROM oracle_slashings WHERE chain_id = $1 AND node_address = $2 AND round_id = 2 AND reason = $3`,
			solanaChainID, svm.Encode(node.Identity()), reason)
	}
	if decided(nodes[2], oraclesvc.ReasonOutlier) != 1 {
		t.Error("the outlier in round 2 was not decided")
	}
	if decided(silent, oraclesvc.ReasonMissedRound) != 1 {
		t.Error("the node silent through round 2's deadline was not decided a miss")
	}
	if total := count(`SELECT count(*) FROM oracle_slashings WHERE chain_id = $1`, solanaChainID); total != 2 {
		t.Errorf("%d slash decisions, want exactly the outlier and the miss", total)
	}

	if handled, err := executor.Step(ctx); err != nil || handled != 2 {
		t.Fatalf("executor handled %d, err %v", handled, err)
	}

	// Executed means the chain confirmed it: the indexer marks a decision executed when it sees
	// NodeSlashed.
	for waited := time.Now(); count(`SELECT count(*) FROM oracle_slashings WHERE chain_id = $1 AND executed_at IS NOT NULL`, solanaChainID) < 2; {
		if time.Since(waited) > 90*time.Second {
			t.Fatal("the slashes were never indexed as executed")
		}
		time.Sleep(2 * time.Second)
		if _, err := idx.Step(ctx); err != nil {
			t.Fatalf("indexer step: %v", err)
		}
	}

	for _, c := range []struct {
		node solanaKey
		want string
	}{{nodes[2], "990000"}, {silent, "995000"}} {
		row, err := s.store.OracleNode(ctx, solanaChainID, svm.Encode(c.node.Identity()))
		if err != nil {
			t.Fatal(err)
		}
		if row.StakedAmount.String() != c.want {
			t.Errorf("%s: stake = %s, want %s after its penalty", svm.Encode(c.node.Identity()), row.StakedAmount, c.want)
		}
	}

	// A retry after a lost confirmation: the chain already holds the slash record, which must close
	// the decision rather than fail it.
	var reset int64
	if err := s.store.QueryRowForTest(ctx,
		`UPDATE oracle_slashings SET submitted_at = NULL, tx_hash = NULL, executed_at = NULL, attempts = 0
		 WHERE chain_id = $1 AND reason = $2 RETURNING 1`,
		solanaChainID, oraclesvc.ReasonOutlier).Scan(&reset); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Step(ctx); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if count(`SELECT count(*) FROM oracle_slashings WHERE chain_id = $1 AND reason = $2 AND abandoned_at IS NOT NULL`, solanaChainID, oraclesvc.ReasonOutlier) != 1 {
		t.Error("the retry did not close the decision the chain had already satisfied")
	}
	row, _ := s.store.OracleNode(ctx, solanaChainID, svm.Encode(nodes[2].Identity()))
	if row.StakedAmount.String() != "990000" {
		t.Errorf("the retry slashed again: stake = %s", row.StakedAmount)
	}
}

func indexThrough(t *testing.T, idx *indexer.Indexer, slot uint64) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for idx.Cursor() < slot {
		if time.Now().After(deadline) {
			t.Fatalf("indexer stalled at slot %d, want %d", idx.Cursor(), slot)
		}
		if _, err := idx.Step(context.Background()); err != nil {
			t.Fatalf("indexer step: %v", err)
		}
	}
}
