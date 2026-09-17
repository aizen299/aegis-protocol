//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/api"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/evm"
	"github.com/aizen299/aegis-protocol/backend/internal/chain/svm"
	governancesvc "github.com/aizen299/aegis-protocol/backend/internal/governance"
	"github.com/aizen299/aegis-protocol/backend/internal/indexer"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/solreceiver"
	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/solvault"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Wormhole's published development guardian, the one the local core on Anvil was deployed with.
const testGuardianKey = "cfb12303a19cde580bb4dd771639b0d26bc68353645571a8cff516ab2ee113a0"

const (
	arbitrumWormholeChain = 23
	// Above the localnet build's floor, and long enough that execution is first tried before it passes.
	receiverDelaySeconds = 15
)

var (
	wormholeCoreBridge = svm.MustIdentity("worm2ZoG2kUd4vFXhvjh93UUH596ayRfgQ2MgjNMTth")
	wormholeShim       = svm.MustIdentity("EFaNWErqAtVWufdNb7yofSHHfWFos843DFpu4JBw24at")
	sysvarClock        = svm.MustIdentity("SysvarC1ock11111111111111111111111111111111")
	sysvarRent         = svm.MustIdentity("SysvarRent111111111111111111111111111111111")
)

type solanaReceiver struct {
	program   pbtypes.Identity
	authority pbtypes.Identity
	admin     solanaKey
}

func (r solanaReceiver) pda(t *testing.T, seeds ...[]byte) pbtypes.Identity {
	return pda(t, r.program, seeds...)
}

func (r solanaReceiver) events(t *testing.T) []accountMeta {
	return []accountMeta{{Key: r.pda(t, []byte("__event_authority"))}, {Key: r.program}}
}

func (r solanaReceiver) message(t *testing.T, sequence uint64) pbtypes.Identity {
	return r.pda(t, []byte("message"), binary.BigEndian.AppendUint16(nil, arbitrumWormholeChain), binary.BigEndian.AppendUint64(nil, sequence))
}

// §18.10: a proposal on Arbitrum to take the Solana vault's admin role is voted, dispatched through
// Wormhole, signed by the test guardian, relayed, waited out, and executed on Solana. The vault's admin
// becomes the receiver's authority, and both chains' indexers and the API show what happened. Forged
// deliveries are refused on the way.
func TestAnArbitrumProposalTakesTheSolanaVaultAdminRoleThroughWormhole(t *testing.T) {
	requireDeps(t)
	upgradeAuthority := requireSolana(t)
	ctx := context.Background()

	d := deployGovernanceRoutedTo(t, solanaChainID)
	s := setupGovernanceStack(t, d)
	delegateVotes(t, deployerKey, d.AegisToken, deployerAddr)

	vaultIDL, err := svm.ParseIDL(solvault.IDL)
	if err != nil {
		t.Fatal(err)
	}
	receiverIDL, err := svm.ParseIDL(solreceiver.IDL)
	if err != nil {
		t.Fatal(err)
	}
	airdrop(t, upgradeAuthority.Identity(), 10_000_000_000)
	initializeCoreBridge(t, upgradeAuthority)
	r := setupReceiver(t, receiverIDL.Program, upgradeAuthority, d.Dispatcher, vaultIDL.Program)
	v, _ := setupSolanaVault(t, vaultIDL.Program, upgradeAuthority, newSolanaKey(t), 0)
	sendOK(t, upgradeAuthority, nil, "confirmed", instruction{
		Program:  v.program,
		Data:     append(anchorDiscriminator("propose_admin"), r.authority[:]...),
		Accounts: append([]accountMeta{{Key: upgradeAuthority.Identity(), Signer: true}, {Key: v.vault, Writable: true}}, v.eventAccounts()...),
	})

	// The action the DAO votes on: the vault's accept_admin, signed by the authority.
	acceptAdmin := append([]accountMeta{{Key: r.authority, Signer: true}, {Key: v.vault, Writable: true}}, v.eventAccounts()...)
	payload := append(votedAccountsHash(acceptAdmin), anchorDiscriminator("accept_admin")...)

	send(t, deployerKey, d.Governor, "propose((uint256,bytes32,uint256,bytes),string,string)",
		"("+big.NewInt(solanaChainID).String()+",0x"+hex.EncodeToString(v.program[:])+",0,0x"+hex.EncodeToString(payload)+")",
		"Take the Solana vault", "The receiver's authority accepts the vault's admin role")
	const proposalID = "1"
	advanceTime(t, votingDelaySeconds+1)
	send(t, deployerKey, d.Governor, "castVote(uint256,uint8,string)", proposalID, "1", "")
	advanceTime(t, votingPeriodSeconds+1)
	send(t, deployerKey, d.Governor, "queue(uint256)", proposalID)
	advanceTime(t, timelockSeconds+1)
	published := executeAndCapturePublication(t, d, proposalID)
	operationID := proposalOperationID(t, d, proposalID)

	emitter := [32]byte{}
	dispatcherBytes, _ := hex.DecodeString(strings.TrimPrefix(d.Dispatcher, "0x"))
	copy(emitter[12:], dispatcherBytes)
	body := vaaBody(published.timestamp, published.nonce, arbitrumWormholeChain, emitter, published.sequence, published.consistency, published.payload)

	// Forgeries first: a refused delivery creates nothing, so the genuine one can follow.
	impostor := strings.Repeat("11", 32)
	if sig, failed := relay(t, r, upgradeAuthority, body, impostor); !failed {
		t.Fatalf("a VAA signed outside the guardian set was received: %s", sig)
	}
	otherEmitter := emitter
	otherEmitter[31] ^= 1
	forged := vaaBody(published.timestamp, published.nonce, arbitrumWormholeChain, otherEmitter, published.sequence, published.consistency, published.payload)
	sig, failed := relay(t, r, upgradeAuthority, forged, testGuardianKey)
	if !failed {
		t.Fatalf("a guardian-signed VAA from another emitter was received: %s", sig)
	}
	requireLogged(t, sig, "UnknownEmitter")

	receivedSig, failed := relay(t, r, upgradeAuthority, body, testGuardianKey)
	if failed {
		t.Fatalf("the genuine VAA was refused:\n%v", transactionLogs(t, receivedSig))
	}
	receivedAt := transactionBlockTime(t, receivedSig)

	execute := executeInstruction(t, r, upgradeAuthority, published.sequence, v.program, acceptAdmin)
	early, failed := sendSolana(t, upgradeAuthority, nil, "confirmed", execute)
	if !failed {
		t.Fatalf("executed before the delay: %s", early)
	}
	requireLogged(t, early, "DelayNotElapsed")

	for time.Now().Unix() <= receivedAt+receiverDelaySeconds+1 {
		time.Sleep(500 * time.Millisecond)
	}
	executedSig := sendOK(t, upgradeAuthority, nil, "finalized", execute)
	if admin := vaultAdmin(t, v.vault); admin != r.authority {
		t.Fatalf("vault admin = %s, want the receiver's authority %s", svm.Encode(admin), svm.Encode(r.authority))
	}
	if _, failed := sendSolana(t, upgradeAuthority, nil, "confirmed", execute); !failed {
		t.Fatal("the message executed twice")
	}

	// Both chains indexed into one database.
	s.indexToHead(t)
	client, err := svm.New(ctx, svm.Options{RPCURL: solanaRPC, ChainID: solanaChainID, Programs: []svm.Registration{{IDL: receiverIDL}}})
	if err != nil {
		t.Fatal(err)
	}
	idx := indexer.New(client, s.store, zerolog.New(io.Discard), indexer.Options{
		ServiceName: "e2e-solana-governance",
		BatchSize:   1_000_000,
	}, indexer.NewGovernanceReceiverHandler(s.store, client, r.program))
	if err := idx.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	indexThrough(t, idx, transactionSlot(t, executedSig))

	actions, err := s.store.ListRemoteActions(ctx, solanaChainID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 {
		t.Fatalf("indexed %d remote actions, want 1: refused deliveries must not appear", len(actions))
	}
	got := actions[0]
	if got.Status != pbtypes.RemoteActionExecuted || got.OperationID.String() != operationID || got.SourceChainID != chainID {
		t.Errorf("remote action = %+v", got)
	}
	if got.Sequence.String() != big.NewInt(int64(published.sequence)).String() || got.Target != svm.Encode(v.program) {
		t.Errorf("sequence %s, target %s", got.Sequence, got.Target)
	}
	if got.ReceivedTx != receivedSig || got.ClosedTx != executedSig || got.ClosedBy != svm.Encode(upgradeAuthority.Identity()) {
		t.Errorf("transactions: received %s, closed %s by %s", got.ReceivedTx, got.ClosedTx, got.ClosedBy)
	}

	cfg := &config.Config{}
	cfg.API.MaxPageSize = 100
	cfg.API.WriteTimeout = 30 * time.Second
	log := zerolog.New(io.Discard)
	srv := api.NewServer(cfg, log, api.Deps{
		Store: s.store,
		Chains: []api.ChainDeps{
			{ID: chainID, Codec: s.client, Governance: governancesvc.NewService(s.store, nil, log, chainID)},
			{ID: solanaChainID, Codec: svm.Codec{}, RemoteGovernance: governancesvc.NewRemoteService(s.store, solanaChainID)},
		},
	}).Handler()

	status, proposal := serve(t, srv, "/v1/governance/proposals/"+proposalID+"?chain=anvil")
	remote, _ := proposal["remote"].(map[string]any)
	if status != http.StatusOK || proposal["state"] != pbtypes.ProposalStateDispatched || remote == nil ||
		remote["status"] != pbtypes.RemoteActionExecuted || remote["chainId"] != float64(solanaChainID) {
		t.Errorf("arbitrum proposal: status %d, state %v, remote %v", status, proposal["state"], proposal["remote"])
	}
	status, listed := serve(t, srv, "/v1/governance/remote-actions?chain=solana-localnet&status=executed")
	items, _ := listed["items"].([]any)
	if status != http.StatusOK || len(items) != 1 {
		t.Errorf("solana remote actions: status %d, body %v", status, listed)
	}
	if status, _ := serve(t, srv, "/v1/governance/remote-actions?chain=anvil"); status != http.StatusNotFound {
		t.Errorf("remote actions on anvil: status %d, want 404", status)
	}
}

// votedAccountsHash is the receiver's account hash: a key listed twice carries the union of its flags
// at every position. docs/v2.0-solana-plan.md §19.
func votedAccountsHash(metas []accountMeta) []byte {
	var buf []byte
	for _, m := range metas {
		signer, writable := m.Signer, m.Writable
		for _, o := range metas {
			if o.Key == m.Key {
				signer, writable = signer || o.Signer, writable || o.Writable
			}
		}
		buf = append(buf, m.Key[:]...)
		buf = append(buf, boolByte(signer), boolByte(writable))
	}
	sum := sha256.Sum256(buf)
	return sum[:]
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

type publication struct {
	sequence    uint64
	nonce       uint32
	consistency uint8
	payload     []byte
	timestamp   uint32
}

// executeAndCapturePublication executes the proposal and reads what Wormhole's core published.
func executeAndCapturePublication(t *testing.T, d governanceDeployment, proposalID string) publication {
	t.Helper()
	receipt := cast(t, "send", "--rpc-url", anvilRPC, "--private-key", deployerKey, "--json", d.Governor, "execute(uint256)", proposalID)
	var parsed struct {
		BlockNumber string `json:"blockNumber"`
		Logs        []struct {
			Address string   `json:"address"`
			Topics  []string `json:"topics"`
			Data    string   `json:"data"`
		} `json:"logs"`
	}
	if err := json.Unmarshal([]byte(receipt), &parsed); err != nil {
		t.Fatalf("receipt: %v\n%s", err, receipt)
	}
	topic := strings.TrimSpace(cast(t, "keccak", "LogMessagePublished(address,uint64,uint32,bytes,uint8)"))
	for _, lg := range parsed.Logs {
		if !strings.EqualFold(lg.Address, d.WormholeCore) || len(lg.Topics) == 0 || lg.Topics[0] != topic {
			continue
		}
		data, err := hex.DecodeString(strings.TrimPrefix(lg.Data, "0x"))
		if err != nil || len(data) < 160 {
			t.Fatalf("log data: %s", lg.Data)
		}
		offset := new(big.Int).SetBytes(data[64:96]).Uint64()
		length := new(big.Int).SetBytes(data[offset : offset+32]).Uint64()
		block := cast(t, "block", "--rpc-url", anvilRPC, "--field", "timestamp", parsed.BlockNumber)
		return publication{
			sequence:    new(big.Int).SetBytes(data[0:32]).Uint64(),
			nonce:       uint32(new(big.Int).SetBytes(data[32:64]).Uint64()),
			consistency: uint8(data[127]),
			payload:     data[offset+32 : offset+32+length],
			timestamp:   uint32(toInt(t, block)),
		}
	}
	t.Fatal("the proposal's execution published no Wormhole message")
	return publication{}
}

// vaaBody is what the guardians sign: the body of a version-1 VAA.
func vaaBody(timestamp, nonce uint32, emitterChain uint16, emitter [32]byte, sequence uint64, consistency uint8, payload []byte) []byte {
	body := binary.BigEndian.AppendUint32(nil, timestamp)
	body = binary.BigEndian.AppendUint32(body, nonce)
	body = binary.BigEndian.AppendUint16(body, emitterChain)
	body = append(body, emitter[:]...)
	body = binary.BigEndian.AppendUint64(body, sequence)
	body = append(body, consistency)
	return append(body, payload...)
}

func initializeCoreBridge(t *testing.T, payer solanaKey) {
	t.Helper()
	guardianSet, _ := guardianSetAddress(t)
	if accountExists(t, guardianSet) {
		return
	}
	guardian, _ := hex.DecodeString(strings.TrimPrefix(strings.ToLower(testGuardianAddr), "0x"))
	data := []byte{0}
	data = binary.LittleEndian.AppendUint32(data, 86_400)
	data = binary.LittleEndian.AppendUint64(data, 0)
	data = binary.LittleEndian.AppendUint32(data, 1)
	data = append(data, guardian...)
	sendOK(t, payer, nil, "confirmed", instruction{Program: wormholeCoreBridge, Data: data, Accounts: []accountMeta{
		{Key: pda(t, wormholeCoreBridge, []byte("Bridge")), Writable: true},
		{Key: guardianSet, Writable: true},
		{Key: pda(t, wormholeCoreBridge, []byte("fee_collector")), Writable: true},
		{Key: payer.Identity(), Signer: true, Writable: true},
		{Key: sysvarClock},
		{Key: sysvarRent},
		{Key: systemProgram},
	}})
}

func guardianSetAddress(t *testing.T) (pbtypes.Identity, uint8) {
	t.Helper()
	id, bump, err := svm.FindProgramAddress([][]byte{[]byte("GuardianSet"), binary.BigEndian.AppendUint32(nil, 0)}, wormholeCoreBridge)
	if err != nil {
		t.Fatal(err)
	}
	return id, bump
}

// setupReceiver initializes the receiver on first use, and on every use points it at this test's
// dispatcher and allows the vault. The upgrade authority can while bootstrap lasts, which the local
// validator never ends.
func setupReceiver(t *testing.T, program pbtypes.Identity, admin solanaKey, dispatcher string, allow pbtypes.Identity) solanaReceiver {
	t.Helper()
	r := solanaReceiver{program: program, admin: admin}
	r.authority = r.pda(t, []byte("authority"))
	config := r.pda(t, []byte("config"))

	if !accountExists(t, config) {
		guardian := newSolanaKey(t).Identity()
		data := anchorDiscriminator("initialize")
		data = binary.LittleEndian.AppendUint64(data, uint64(solanaChainID))
		data = binary.LittleEndian.AppendUint16(data, arbitrumWormholeChain)
		data = append(data, make([]byte, 32)...)
		data[len(data)-1] = 1
		data = append(data, guardian[:]...)
		data = binary.LittleEndian.AppendUint64(data, receiverDelaySeconds)
		data = binary.LittleEndian.AppendUint64(data, 86_400)
		accounts := []accountMeta{
			{Key: admin.Identity(), Signer: true, Writable: true},
			{Key: config, Writable: true},
			{Key: r.authority},
			{Key: program},
			{Key: pda(t, bpfLoaderUpgrade, program[:])},
			{Key: systemProgram},
		}
		sendOK(t, admin, nil, "confirmed", instruction{Program: program, Data: data, Accounts: append(accounts, r.events(t)...)})
	}

	govern := func(data []byte) instruction {
		accounts := []accountMeta{{Key: admin.Identity(), Signer: true}, {Key: config, Writable: true}}
		return instruction{Program: program, Data: data, Accounts: append(accounts, r.events(t)...)}
	}
	dispatcherBytes, _ := hex.DecodeString(strings.TrimPrefix(dispatcher, "0x"))
	emitter := append(make([]byte, 12), dispatcherBytes...)
	setEmitter := binary.LittleEndian.AppendUint16(anchorDiscriminator("set_emitter"), arbitrumWormholeChain)
	setDelay := binary.LittleEndian.AppendUint64(anchorDiscriminator("set_delay"), receiverDelaySeconds)
	allowData := append(anchorDiscriminator("set_allowed"), allow[:]...)
	allowAccounts := []accountMeta{
		{Key: admin.Identity(), Signer: true, Writable: true},
		{Key: config},
		{Key: r.pda(t, []byte("allowed"), allow[:]), Writable: true},
		{Key: systemProgram},
	}
	sendOK(t, admin, nil, "confirmed",
		govern(append(setEmitter, emitter...)),
		govern(setDelay),
		instruction{Program: program, Data: append(allowData, 1), Accounts: append(allowAccounts, r.events(t)...)},
	)
	return r
}

// relay posts guardian signatures to Wormhole's shim and delivers the VAA to the receiver, as any
// relayer would. It returns the delivery's signature and whether it failed.
func relay(t *testing.T, r solanaReceiver, payer solanaKey, body []byte, guardianKey string) (string, bool) {
	t.Helper()
	key, err := evm.NodeKeyFromHex(guardianKey)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := evm.SignVAABody(key, body)
	if err != nil {
		t.Fatal(err)
	}
	entry := append([]byte{0}, signature...)

	signatures := newSolanaKey(t)
	post := anchorDiscriminator("post_signatures")
	post = binary.LittleEndian.AppendUint32(post, 0)
	post = append(post, 1)
	post = binary.LittleEndian.AppendUint32(post, 1)
	post = append(post, entry...)
	sendOK(t, payer, []solanaKey{signatures}, "confirmed", instruction{Program: wormholeShim, Data: post, Accounts: []accountMeta{
		{Key: payer.Identity(), Signer: true, Writable: true},
		{Key: signatures.Identity(), Signer: true, Writable: true},
		{Key: systemProgram},
	}})

	emitterChain := binary.BigEndian.Uint16(body[8:10])
	sequence := binary.BigEndian.Uint64(body[42:50])
	guardianSet, bump := guardianSetAddress(t)
	data := anchorDiscriminator("receive_message")
	data = binary.LittleEndian.AppendUint16(data, emitterChain)
	data = binary.LittleEndian.AppendUint64(data, sequence)
	data = append(data, bump)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(body)))
	data = append(data, body...)
	accounts := []accountMeta{
		{Key: payer.Identity(), Signer: true, Writable: true},
		{Key: r.pda(t, []byte("config"))},
		{Key: r.message(t, sequence), Writable: true},
		{Key: guardianSet},
		{Key: signatures.Identity()},
		{Key: wormholeShim},
		{Key: systemProgram},
	}
	return sendSolana(t, payer, nil, "confirmed", instruction{Program: r.program, Data: data, Accounts: append(accounts, r.events(t)...)})
}

func executeInstruction(t *testing.T, r solanaReceiver, executor solanaKey, sequence uint64, target pbtypes.Identity, action []accountMeta) instruction {
	t.Helper()
	accounts := []accountMeta{
		{Key: executor.Identity(), Signer: true},
		{Key: r.pda(t, []byte("config"))},
		{Key: r.message(t, sequence), Writable: true},
		{Key: r.pda(t, []byte("allowed"), target[:])},
		{Key: target},
		{Key: r.authority},
	}
	accounts = append(accounts, r.events(t)...)
	for _, m := range action {
		accounts = append(accounts, accountMeta{Key: m.Key, Writable: m.Writable})
	}
	return instruction{Program: r.program, Data: anchorDiscriminator("execute"), Accounts: accounts}
}

func accountExists(t *testing.T, address pbtypes.Identity) bool {
	t.Helper()
	var info struct {
		Value *struct{} `json:"value"`
	}
	if err := solanaCall(context.Background(), "getAccountInfo", []any{svm.Encode(address), map[string]any{"commitment": "confirmed", "encoding": "base64"}}, &info); err != nil {
		t.Fatal(err)
	}
	return info.Value != nil
}

// vaultAdmin reads the admin field at the offset the committed layout baseline records.
func vaultAdmin(t *testing.T, vault pbtypes.Identity) pbtypes.Identity {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "solana", "layouts", "aegis_vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	var layouts map[string]struct {
		Fields []struct {
			Name   string `json:"name"`
			Offset int    `json:"offset"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &layouts); err != nil {
		t.Fatal(err)
	}
	offset := -1
	for _, f := range layouts["Vault"].Fields {
		if f.Name == "admin" {
			offset = f.Offset
		}
	}
	var info struct {
		Value struct {
			Data []string `json:"data"`
		} `json:"value"`
	}
	if err := solanaCall(context.Background(), "getAccountInfo", []any{svm.Encode(vault), map[string]any{"commitment": "confirmed", "encoding": "base64"}}, &info); err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(info.Value.Data[0])
	if err != nil || offset < 0 || len(data) < offset+32 {
		t.Fatalf("vault account: offset %d, %d bytes, %v", offset, len(data), err)
	}
	var admin pbtypes.Identity
	copy(admin[:], data[offset:offset+32])
	return admin
}

func transactionLogs(t *testing.T, sig string) []string {
	t.Helper()
	var tx struct {
		Meta struct {
			LogMessages []string `json:"logMessages"`
		} `json:"meta"`
	}
	if err := solanaCall(context.Background(), "getTransaction", []any{sig, map[string]any{"commitment": "confirmed", "encoding": "json"}}, &tx); err != nil {
		t.Fatal(err)
	}
	return tx.Meta.LogMessages
}

func requireLogged(t *testing.T, sig, want string) {
	t.Helper()
	logs := transactionLogs(t, sig)
	for _, l := range logs {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Fatalf("transaction %s failed, but not for %s:\n%s", sig, want, strings.Join(logs, "\n"))
}

func transactionBlockTime(t *testing.T, sig string) int64 {
	t.Helper()
	var tx struct {
		BlockTime int64 `json:"blockTime"`
	}
	if err := solanaCall(context.Background(), "getTransaction", []any{sig, map[string]any{"commitment": "confirmed", "encoding": "json"}}, &tx); err != nil {
		t.Fatal(err)
	}
	return tx.BlockTime
}
