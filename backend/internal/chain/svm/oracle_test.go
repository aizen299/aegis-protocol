package svm

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// The same hex is asserted by the oracle program's Rust tests (signature.rs, mod vector): Go signs
// what Rust verifies, so the layout is pinned on both sides.
const sharedSubmissionVector = "61656769732d6f7261636c652d7375626d697373696f6e2d7631111111111111111111111111111111111111111111111111111111111111111103000000000000400700000000000000333333333333333333333333333333333333333333333333333333333333333300505a4f7e9f4eb1060000000000000022222222222222222222222222222222222222222222222222222222222222220500000000000000"

func fill(b byte) pbtypes.Identity {
	var id pbtypes.Identity
	for i := range id {
		id[i] = b
	}
	return id
}

func TestSubmissionMessageMatchesTheProgramsVector(t *testing.T) {
	value, _ := new(big.Int).SetString("123456789000000000000", 10)
	msg, err := SubmissionMessage(fill(0x11), (1<<62)+3, 7, [32]byte(fill(0x33)), value, fill(0x22), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(msg); got != sharedSubmissionVector {
		t.Fatalf("message = %s\nwant      %s", got, sharedSubmissionVector)
	}
}

func TestSubmissionMessageRefusesValuesOutsideAU128(t *testing.T) {
	for _, v := range []*big.Int{big.NewInt(-1), new(big.Int).Lsh(big.NewInt(1), 128), nil} {
		if _, err := SubmissionMessage(fill(1), 1, 1, [32]byte{}, v, fill(2), 0); err == nil {
			t.Errorf("value %v accepted", v)
		}
	}
	if _, err := SubmissionMessage(fill(1), 1, 1, [32]byte{}, maxU128, fill(2), 0); err != nil {
		t.Errorf("u128 max refused: %v", err)
	}
}

func TestEd25519InstructionIsTheLayoutTheProgramAccepts(t *testing.T) {
	key, _ := GenerateKeypair()
	msg := []byte("attestation")
	ix := ed25519Instruction(key, msg)
	if ix.Program != ed25519Program || len(ix.Accounts) != 0 {
		t.Fatal("not an accountless Ed25519 program instruction")
	}
	d := ix.Data
	if d[0] != 1 {
		t.Fatalf("signature count = %d", d[0])
	}
	u16 := func(at int) int { return int(binary.LittleEndian.Uint16(d[at:])) }
	for _, at := range []int{4, 8, 14} {
		if u16(at) != 0xffff {
			t.Errorf("instruction index at %d = %d, want this instruction's own data", at, u16(at))
		}
	}
	sig, pub, message := d[u16(2):u16(2)+64], d[u16(6):u16(6)+32], d[u16(10):u16(10)+u16(12)]
	id := key.Identity()
	if string(pub) != string(id[:]) || string(message) != string(msg) {
		t.Fatal("offsets do not point at the key and message")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), message, sig) {
		t.Fatal("signature does not verify")
	}
}

// encodeAccount lays out an account the way the program does, from the IDL, so the node's reads are
// tested against real offsets.
func encodeAccount(t *testing.T, idl *IDL, name string, values map[string]any) []byte {
	t.Helper()
	l := idl.accounts[name]
	data := append([]byte{}, l.discriminator...)
	for _, f := range l.fields {
		chunk := make([]byte, f.size)
		switch v := values[f.name].(type) {
		case pbtypes.Identity:
			copy(chunk, v[:])
		case [32]byte:
			copy(chunk, v[:])
		case bool:
			if v {
				chunk[0] = 1
			}
		case uint8:
			chunk[0] = v
		case int64:
			binary.LittleEndian.PutUint64(chunk, uint64(v))
		case uint64:
			binary.LittleEndian.PutUint64(chunk, v)
		}
		data = append(data, chunk...)
	}
	return data
}

func (n *fakeNode) setAccount(owner, address pbtypes.Identity, data []byte) {
	n.accounts[Encode(address)] = accountInfo{Owner: Encode(owner), Data: []string{base64.StdEncoding.EncodeToString(data), "base64"}}
}

func oracleHarness(t *testing.T) (*fakeNode, *OracleNodeChain, Keypair) {
	t.Helper()
	idl := oracleIDL(t)
	node := &fakeNode{txs: map[string]fakeTx{}, accounts: map[string]accountInfo{}, calls: map[string]int{}}
	srv := node.serve(t)
	t.Cleanup(srv.Close)
	client, err := newClient(Options{RPCURL: srv.URL, ChainID: pbtypes.ChainIDSolanaLocalnet, Programs: []Registration{{IDL: idl}}})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := GenerateKeypair()
	chain, err := NewOracleNodeChain(client, key)
	if err != nil {
		t.Fatal(err)
	}
	return node, chain, key
}

func TestCurrentRoundAnswersFromTheProgramsAccounts(t *testing.T) {
	fake, chain, key := oracleHarness(t)
	program := chain.program
	feed := [32]byte{0xfe}
	feedHex := "0x" + hex.EncodeToString(feed[:])
	me := key.Identity()
	roundSeed := binary.LittleEndian.AppendUint64(nil, 4)

	view, err := chain.CurrentRound(context.Background(), feedHex)
	if err != nil || view.Open {
		t.Fatalf("no feed: view = %+v, err = %v", view, err)
	}

	fake.setAccount(program, chain.pda([]byte("feed"), feed[:]), encodeAccount(t, chain.idl, "Feed", map[string]any{
		"registered": true, "decimals": uint8(8), "feedId": feed, "currentRoundId": uint64(4),
	}))
	fake.setAccount(program, chain.pda([]byte("round"), roundSeed), encodeAccount(t, chain.idl, "Round", map[string]any{
		"state": uint8(roundOpen), "roundId": uint64(4), "deadline": int64(1_900_000_000), "nodeSetVersion": uint64(10),
	}))

	view, err = chain.CurrentRound(context.Background(), feedHex)
	if err != nil || !view.Open || view.Eligible || view.Submitted || view.Decimals != 8 || view.RoundID.String() != "4" {
		t.Fatalf("unregistered node: view = %+v, err = %v", view, err)
	}

	nodeAddress := chain.pda([]byte("node"), me[:])
	fake.setAccount(program, nodeAddress, encodeAccount(t, chain.idl, "Node", map[string]any{
		"active": true, "node": me, "activatedAtVersion": uint64(11),
	}))
	if view, err = chain.CurrentRound(context.Background(), feedHex); err != nil || view.Eligible {
		t.Fatal("a node activated after the round froze its set was reported eligible")
	}

	fake.setAccount(program, nodeAddress, encodeAccount(t, chain.idl, "Node", map[string]any{
		"active": true, "node": me, "activatedAtVersion": uint64(10),
	}))
	if view, _ = chain.CurrentRound(context.Background(), feedHex); !view.Eligible {
		t.Fatal("a node active at the frozen version was reported ineligible")
	}

	fake.setAccount(program, chain.pda([]byte("submission"), roundSeed, me[:]), encodeAccount(t, chain.idl, "Submission", map[string]any{}))
	if view, _ = chain.CurrentRound(context.Background(), feedHex); !view.Submitted {
		t.Fatal("an existing submission was not reported")
	}

	fake.setAccount(TokenProgram, nodeAddress, encodeAccount(t, chain.idl, "Node", map[string]any{"active": true, "node": me}))
	if _, err := chain.CurrentRound(context.Background(), feedHex); err == nil {
		t.Fatal("a node account owned by another program was read")
	}
}

// Every attestation is signed over the program's configured chain. A node pointed at another
// cluster's deployment must stop rather than send attestations the program will refuse.
func TestSubmitRefusesAProgramConfiguredForAnotherChain(t *testing.T) {
	fake, chain, _ := oracleHarness(t)
	me := chain.key.Identity()
	// Everything else Submit reads is present, so only the chain id can stop it.
	fake.setAccount(chain.program, chain.pda([]byte("node"), me[:]), encodeAccount(t, chain.idl, "Node", map[string]any{
		"active": true, "node": me,
	}))
	fake.setAccount(chain.program, chain.pda([]byte("config")), encodeAccount(t, chain.idl, "Config", map[string]any{
		"chainId": pbtypes.ChainIDSolanaDevnet,
	}))
	_, err := chain.Submit(context.Background(), pbtypes.NewRaw(big.NewInt(1)), "0x"+hex.EncodeToString(make([]byte, 32)), pbtypes.NewRaw(big.NewInt(5)))
	if err == nil || !strings.Contains(err.Error(), "configured for chain") {
		t.Fatalf("err = %v, simulations = %d", err, fake.calls["simulateTransaction"])
	}
}

// Vector from `cast keccak "ETH/USD"`, the derivation the Arbitrum node uses.
func TestFeedIDMatchesArbitrums(t *testing.T) {
	id := FeedID("ETH/USD")
	if got := "0x" + hex.EncodeToString(id[:]); got != "0x0b43555ace6b39aae1b894097d0a9fc17f504c62fea598fa206cc6f5088e6e45" {
		t.Fatalf("feed id = %s", got)
	}
}
