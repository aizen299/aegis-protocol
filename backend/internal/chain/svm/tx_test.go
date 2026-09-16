package svm

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

func keypairJSON(t *testing.T, bytes []byte) string {
	t.Helper()
	ints := make([]int, len(bytes))
	for i, b := range bytes {
		ints[i] = int(b)
	}
	out, _ := json.Marshal(ints)
	return string(out)
}

func TestKeypairFromJSONAcceptsOnlyAConsistentSolanaKeygenKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	good := []byte(priv)

	k, err := KeypairFromJSON(keypairJSON(t, good))
	if err != nil {
		t.Fatal(err)
	}
	if k.Identity() != pbtypes.Identity(priv.Public().(ed25519.PublicKey)) {
		t.Fatal("identity is not the public key")
	}

	mismatched := append([]byte{}, good...)
	mismatched[63] ^= 1
	for name, input := range map[string]string{
		"hex":            "0x" + strings.Repeat("ab", 32),
		"31-byte seed":   keypairJSON(t, good[:31]),
		"32-byte seed":   keypairJSON(t, good[:32]),
		"mismatched pub": keypairJSON(t, mismatched),
		"out of range":   strings.Replace(keypairJSON(t, good), "[", "[256,", 1),
		"empty":          "",
	} {
		if _, err := KeypairFromJSON(input); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCompactU16(t *testing.T) {
	cases := map[int][]byte{0: {0}, 127: {0x7f}, 128: {0x80, 0x01}, 16383: {0xff, 0x7f}, 16384: {0x80, 0x80, 0x01}}
	for n, want := range cases {
		if got := compactU16(n); string(got) != string(want) {
			t.Errorf("compactU16(%d) = %x, want %x", n, got, want)
		}
	}
}

// The runtime rejects a transaction whose accounts are out of order or whose signatures do not cover
// the message, so both are checked on a transaction with every kind of account.
func TestBuildTransactionOrdersAccountsAndSignsTheMessage(t *testing.T) {
	payer, _ := GenerateKeypair()
	cosigner, _ := GenerateKeypair()
	program := pbtypes.Identity{9}
	writable, readonly := pbtypes.Identity{3}, pbtypes.Identity{4}

	ix := Instruction{
		Program: program,
		Accounts: []AccountMeta{
			{Key: readonly},
			{Key: cosigner.Identity(), Signer: true},
			{Key: writable, Writable: true},
			{Key: payer.Identity(), Signer: true, Writable: true},
		},
		Data: []byte{1, 2, 3},
	}
	raw, err := BuildTransaction(pbtypes.Identity{7}, payer, []Keypair{cosigner}, []Instruction{ix})
	if err != nil {
		t.Fatal(err)
	}

	if raw[0] != 2 {
		t.Fatalf("signature count = %d, want 2", raw[0])
	}
	msg := raw[1+2*64:]
	if msg[0] != 2 || msg[1] != 1 || msg[2] != 2 {
		t.Fatalf("header = %v, want 2 signers, 1 readonly signer, 2 readonly unsigned", msg[:3])
	}
	keys := msg[4 : 4+5*32]
	order := []pbtypes.Identity{payer.Identity(), cosigner.Identity(), writable, readonly, program}
	for i, want := range order {
		if string(keys[i*32:(i+1)*32]) != string(want[:]) {
			t.Errorf("account %d out of order", i)
		}
	}
	for i, signer := range []Keypair{payer, cosigner} {
		sig := raw[1+i*64 : 1+(i+1)*64]
		if !ed25519.Verify(signer.private.Public().(ed25519.PublicKey), msg, sig) {
			t.Errorf("signature %d does not verify over the message", i)
		}
	}

	if _, err := BuildTransaction(pbtypes.Identity{7}, payer, nil, []Instruction{ix}); err == nil {
		t.Error("built without the cosigner's key")
	}
}
