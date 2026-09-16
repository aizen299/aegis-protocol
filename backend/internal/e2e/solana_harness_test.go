//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain/svm"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// solanaRPC is the validator scripts/e2e.sh starts, with the vault program loaded at its declared id
// and SOLANA_E2E_AUTHORITY as its upgrade authority.
var solanaRPC = envOr("SOLANA_RPC", "http://127.0.0.1:8899")

var (
	systemProgram    = svm.MustIdentity("11111111111111111111111111111111")
	bpfLoaderUpgrade = svm.MustIdentity("BPFLoaderUpgradeab1e11111111111111111111111")
)

type solanaKey struct {
	priv ed25519.PrivateKey
}

func newSolanaKey(t *testing.T) solanaKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return solanaKey{priv: priv}
}

func (k solanaKey) id() pbtypes.Identity {
	id, _ := pbtypes.IdentityFromBytes(k.priv.Public().(ed25519.PublicKey))
	return id
}

// requireSolana skips when no validator is running, unless SOLANA_E2E=1 says one must be: CI sets it,
// so a validator that failed to start fails the suite instead of passing it by skipping.
func requireSolana(t *testing.T) solanaKey {
	t.Helper()
	required := os.Getenv("SOLANA_E2E") == "1"
	var version map[string]any
	if err := solanaCall(context.Background(), "getVersion", nil, &version); err != nil {
		if required {
			t.Fatalf("solana validator unreachable at %s: %v", solanaRPC, err)
		}
		t.Skipf("solana validator unreachable at %s; run `make e2e`", solanaRPC)
	}
	path := os.Getenv("SOLANA_E2E_AUTHORITY")
	if path == "" {
		t.Fatal("SOLANA_E2E_AUTHORITY is unset; it names the vault program's upgrade authority keypair")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read authority keypair: %v", err)
	}
	var bytes []byte
	if err := json.Unmarshal(raw, &bytes); err != nil || len(bytes) != ed25519.PrivateKeySize {
		t.Fatalf("authority keypair %s is not a 64-byte keypair", path)
	}
	return solanaKey{priv: ed25519.PrivateKey(bytes)}
}

func solanaCall(ctx context.Context, method string, params []any, out any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, solanaRPC, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Error != nil {
		return fmt.Errorf("%s: %s", method, envelope.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

type accountMeta struct {
	key      pbtypes.Identity
	signer   bool
	writable bool
}

type instruction struct {
	program  pbtypes.Identity
	accounts []accountMeta
	data     []byte
}

func compactLen(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n == 0 {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

// buildTransaction serialises a legacy transaction: accounts ordered signer-writable,
// signer-readonly, writable, readonly, with the fee payer first.
func buildTransaction(t *testing.T, payer solanaKey, signers []solanaKey, ixs []instruction) []byte {
	t.Helper()
	type entry struct {
		accountMeta
		order int
	}
	merged := map[pbtypes.Identity]*entry{payer.id(): {accountMeta{payer.id(), true, true}, 0}}
	add := func(m accountMeta) {
		if e, ok := merged[m.key]; ok {
			e.signer = e.signer || m.signer
			e.writable = e.writable || m.writable
			return
		}
		merged[m.key] = &entry{m, len(merged)}
	}
	for _, ix := range ixs {
		for _, a := range ix.accounts {
			add(a)
		}
		add(accountMeta{key: ix.program})
	}
	entries := make([]*entry, 0, len(merged))
	for _, e := range merged {
		entries = append(entries, e)
	}
	rank := func(e *entry) int {
		switch {
		case e.key == payer.id():
			return 0
		case e.signer && e.writable:
			return 1
		case e.signer:
			return 2
		case e.writable:
			return 3
		}
		return 4
	}
	sort.Slice(entries, func(i, j int) bool {
		if rank(entries[i]) != rank(entries[j]) {
			return rank(entries[i]) < rank(entries[j])
		}
		return entries[i].order < entries[j].order
	})

	index := map[pbtypes.Identity]int{}
	var numSigners, readonlySigned, readonlyUnsigned int
	for i, e := range entries {
		index[e.key] = i
		if e.signer {
			numSigners++
			if !e.writable {
				readonlySigned++
			}
		} else if !e.writable {
			readonlyUnsigned++
		}
	}

	var blockhash struct {
		Value struct {
			Blockhash string `json:"blockhash"`
		} `json:"value"`
	}
	if err := solanaCall(context.Background(), "getLatestBlockhash", []any{map[string]any{"commitment": "confirmed"}}, &blockhash); err != nil {
		t.Fatal(err)
	}
	recent := svm.MustIdentity(blockhash.Value.Blockhash)

	msg := []byte{byte(numSigners), byte(readonlySigned), byte(readonlyUnsigned)}
	msg = append(msg, compactLen(len(entries))...)
	for _, e := range entries {
		msg = append(msg, e.key[:]...)
	}
	msg = append(msg, recent[:]...)
	msg = append(msg, compactLen(len(ixs))...)
	for _, ix := range ixs {
		msg = append(msg, byte(index[ix.program]))
		msg = append(msg, compactLen(len(ix.accounts))...)
		for _, a := range ix.accounts {
			msg = append(msg, byte(index[a.key]))
		}
		msg = append(msg, compactLen(len(ix.data))...)
		msg = append(msg, ix.data...)
	}

	keys := map[pbtypes.Identity]solanaKey{payer.id(): payer}
	for _, s := range signers {
		keys[s.id()] = s
	}
	tx := compactLen(numSigners)
	for _, e := range entries[:numSigners] {
		k, ok := keys[e.key]
		if !ok {
			t.Fatalf("no key for signer %s", svm.Encode(e.key))
		}
		tx = append(tx, ed25519.Sign(k.priv, msg)...)
	}
	return append(tx, msg...)
}

// sendSolana submits without preflight, so a transaction that fails still lands on chain, and waits
// for the given commitment. It returns the signature and whether execution failed.
func sendSolana(t *testing.T, payer solanaKey, signers []solanaKey, commitment string, ixs ...instruction) (string, bool) {
	t.Helper()
	raw := buildTransaction(t, payer, signers, ixs)
	var sig string
	cfg := map[string]any{"encoding": "base64", "skipPreflight": true, "preflightCommitment": "confirmed"}
	if err := solanaCall(context.Background(), "sendTransaction", []any{base64.StdEncoding.EncodeToString(raw), cfg}, &sig); err != nil {
		t.Fatalf("send transaction: %v", err)
	}
	return sig, waitForSignature(t, sig, commitment)
}

func waitForSignature(t *testing.T, sig, commitment string) bool {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var statuses struct {
			Value []*struct {
				Err                json.RawMessage `json:"err"`
				ConfirmationStatus string          `json:"confirmationStatus"`
			} `json:"value"`
		}
		if err := solanaCall(context.Background(), "getSignatureStatuses", []any{[]string{sig}, map[string]any{"searchTransactionHistory": true}}, &statuses); err != nil {
			t.Fatal(err)
		}
		if s := statuses.Value[0]; s != nil {
			reached := s.ConfirmationStatus == "finalized" || (commitment == "confirmed" && s.ConfirmationStatus == "confirmed")
			if reached {
				return len(s.Err) > 0 && string(s.Err) != "null"
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	t.Fatalf("transaction %s did not reach %s", sig, commitment)
	return false
}

func airdrop(t *testing.T, to pbtypes.Identity, lamports uint64) {
	t.Helper()
	var sig string
	if err := solanaCall(context.Background(), "requestAirdrop", []any{svm.Encode(to), lamports, map[string]any{"commitment": "confirmed"}}, &sig); err != nil {
		t.Fatalf("airdrop: %v", err)
	}
	waitForSignature(t, sig, "confirmed")
}

func rentExempt(t *testing.T, size int) uint64 {
	t.Helper()
	var lamports uint64
	if err := solanaCall(context.Background(), "getMinimumBalanceForRentExemption", []any{size}, &lamports); err != nil {
		t.Fatal(err)
	}
	return lamports
}

func u64le(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }

func createAccount(t *testing.T, payer, account solanaKey, size int, owner pbtypes.Identity) instruction {
	data := binary.LittleEndian.AppendUint32(nil, 0)
	data = append(data, u64le(rentExempt(t, size))...)
	data = append(data, u64le(uint64(size))...)
	data = append(data, owner[:]...)
	return instruction{program: systemProgram, data: data, accounts: []accountMeta{
		{payer.id(), true, true}, {account.id(), true, true},
	}}
}

func initializeMint(mint, authority pbtypes.Identity, decimals byte) instruction {
	data := append([]byte{20, decimals}, authority[:]...)
	data = append(data, 0)
	return instruction{program: svm.TokenProgram, data: data, accounts: []accountMeta{{key: mint, writable: true}}}
}

func initializeTokenAccount(account, mint, owner pbtypes.Identity) instruction {
	return instruction{program: svm.TokenProgram, data: append([]byte{18}, owner[:]...), accounts: []accountMeta{
		{key: account, writable: true}, {key: mint},
	}}
}

func mintTo(mint, to pbtypes.Identity, authority solanaKey, amount uint64) instruction {
	return instruction{program: svm.TokenProgram, data: append([]byte{7}, u64le(amount)...), accounts: []accountMeta{
		{key: mint, writable: true}, {key: to, writable: true}, {authority.id(), true, false},
	}}
}

func anchorDiscriminator(name string) []byte {
	sum := sha256.Sum256([]byte("global:" + name))
	return sum[:8]
}

func pda(t *testing.T, program pbtypes.Identity, seeds ...[]byte) pbtypes.Identity {
	t.Helper()
	id, _, err := svm.FindProgramAddress(seeds, program)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type solanaVault struct {
	program     pbtypes.Identity
	mint        pbtypes.Identity
	vault       pbtypes.Identity
	vaultTokens pbtypes.Identity
	authority   solanaKey
}

func (v solanaVault) eventAccounts() []accountMeta {
	authority, _, _ := svm.FindProgramAddress([][]byte{[]byte("__event_authority")}, v.program)
	return []accountMeta{{key: authority}, {key: v.program}}
}

func (v solanaVault) position(owner pbtypes.Identity) pbtypes.Identity {
	id, _, _ := svm.FindProgramAddress([][]byte{[]byte("position"), v.vault[:], owner[:]}, v.program)
	return id
}

// setupSolanaVault creates a six-decimal mint and its vault, and funds a depositor.
func setupSolanaVault(t *testing.T, program pbtypes.Identity, authority solanaKey, depositor solanaKey, funded uint64) (solanaVault, pbtypes.Identity) {
	t.Helper()
	airdrop(t, authority.id(), 10_000_000_000)
	airdrop(t, depositor.id(), 10_000_000_000)

	mint, depositorTokens := newSolanaKey(t), newSolanaKey(t)
	sendOK(t, authority, []solanaKey{mint, depositorTokens}, "confirmed",
		createAccount(t, authority, mint, 82, svm.TokenProgram),
		initializeMint(mint.id(), authority.id(), 6),
		createAccount(t, authority, depositorTokens, 165, svm.TokenProgram),
		initializeTokenAccount(depositorTokens.id(), mint.id(), depositor.id()),
		mintTo(mint.id(), depositorTokens.id(), authority, funded),
	)

	v := solanaVault{program: program, mint: mint.id(), authority: authority}
	v.vault = pda(t, program, []byte("vault"), v.mint[:])
	v.vaultTokens = pda(t, program, []byte("tokens"), v.vault[:])

	manager, pauser := newSolanaKey(t).id(), newSolanaKey(t).id()
	data := anchorDiscriminator("initialize_vault")
	data = append(data, manager[:]...)
	data = append(data, pauser[:]...)
	data = append(data, u64le(0)...)
	data = append(data, u64le(0)...)
	accounts := []accountMeta{
		{authority.id(), true, true},
		{key: v.vault, writable: true},
		{key: v.vaultTokens, writable: true},
		{key: v.mint},
		{key: program},
		{key: pda(t, bpfLoaderUpgrade, program[:])},
		{key: svm.TokenProgram},
		{key: systemProgram},
	}
	accounts = append(accounts, v.eventAccounts()...)
	sendOK(t, authority, nil, "confirmed", instruction{program: program, data: data, accounts: accounts})
	return v, depositorTokens.id()
}

func (v solanaVault) depositIx(depositor solanaKey, from pbtypes.Identity, amount uint64, minShares uint64) instruction {
	data := anchorDiscriminator("deposit")
	data = append(data, u64le(amount)...)
	data = append(data, u64le(minShares)...)
	data = append(data, make([]byte, 8)...)
	accounts := []accountMeta{
		{depositor.id(), true, true},
		{key: depositor.id()},
		{key: v.vault, writable: true},
		{key: v.vaultTokens, writable: true},
		{key: from, writable: true},
		{key: v.position(depositor.id()), writable: true},
		{key: svm.TokenProgram},
		{key: systemProgram},
	}
	return instruction{program: v.program, data: data, accounts: append(accounts, v.eventAccounts()...)}
}

func sendOK(t *testing.T, payer solanaKey, signers []solanaKey, commitment string, ixs ...instruction) string {
	t.Helper()
	sig, failed := sendSolana(t, payer, signers, commitment, ixs...)
	if failed {
		var tx struct {
			Meta struct {
				LogMessages []string `json:"logMessages"`
			} `json:"meta"`
		}
		_ = solanaCall(context.Background(), "getTransaction", []any{sig, map[string]any{"commitment": "confirmed", "encoding": "json"}}, &tx)
		t.Fatalf("transaction %s failed:\n%v", sig, tx.Meta.LogMessages)
	}
	return sig
}
