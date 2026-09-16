package svm

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

func raw(n int64) pbtypes.Raw { return pbtypes.NewRaw(big.NewInt(n)) }

func TestTheVerifierAcceptsOnlyTheExactAttestation(t *testing.T) {
	program := fill(0x11)
	key, _ := GenerateKeypair()
	node := Encode(key.Identity())
	feed := [32]byte{0xfe}
	feedHex := "0x" + hex.EncodeToString(feed[:])
	chain := pbtypes.ChainIDSolanaLocalnet

	message, _ := SubmissionMessage(program, chain, 3, feed, big.NewInt(500), key.Identity(), 2)
	sig := key.Sign(message)
	v := NewSubmissionVerifier(program)

	if ok, err := v.Verify(chain, raw(3), feedHex, raw(500), node, raw(2), sig); err != nil || !ok {
		t.Fatalf("genuine attestation: ok = %v, err = %v", ok, err)
	}

	other, _ := GenerateKeypair()
	cases := map[string]func() (bool, error){
		"another value": func() (bool, error) { return v.Verify(chain, raw(3), feedHex, raw(501), node, raw(2), sig) },
		"another round": func() (bool, error) { return v.Verify(chain, raw(4), feedHex, raw(500), node, raw(2), sig) },
		"another nonce": func() (bool, error) { return v.Verify(chain, raw(3), feedHex, raw(500), node, raw(3), sig) },
		"another chain": func() (bool, error) { return v.Verify(chain+1, raw(3), feedHex, raw(500), node, raw(2), sig) },
		"another node": func() (bool, error) {
			return v.Verify(chain, raw(3), feedHex, raw(500), Encode(other.Identity()), raw(2), sig)
		},
		"another program": func() (bool, error) {
			return NewSubmissionVerifier(fill(0x12)).Verify(chain, raw(3), feedHex, raw(500), node, raw(2), sig)
		},
		"short signature": func() (bool, error) { return v.Verify(chain, raw(3), feedHex, raw(500), node, raw(2), sig[:63]) },
	}
	for name, check := range cases {
		ok, err := check()
		if err != nil || ok {
			t.Errorf("%s: ok = %v, err = %v; want a clean false", name, ok, err)
		}
	}

	if _, err := v.Verify(chain, raw(3), feedHex, raw(500), "0xnotbase58", raw(2), sig); err == nil {
		t.Error("an unreadable node address did not error")
	}
	huge := pbtypes.NewRaw(new(big.Int).Lsh(big.NewInt(1), 64))
	if _, err := v.Verify(chain, huge, feedHex, raw(500), node, raw(2), sig); err == nil {
		t.Error("a round id beyond u64 did not error")
	}
}

func slasherHarness(t *testing.T) (*fakeNode, *Slasher, Keypair) {
	t.Helper()
	fake, chain, _ := oracleHarness(t)
	key, _ := GenerateKeypair()
	slasher, err := NewSlasher(chain.client, chain.program, key)
	if err != nil {
		t.Fatal(err)
	}
	return fake, slasher, key
}

func TestSlashSendsTheInstructionTheProgramExpects(t *testing.T) {
	fake, slasher, key := slasherHarness(t)
	node, _ := GenerateKeypair()

	sig, err := slasher.Slash(context.Background(), Encode(node.Identity()), raw(9), raw(1_000), "MISSED_ROUND")
	if err != nil || sig != signature(77) {
		t.Fatalf("sig = %s, err = %v", sig, err)
	}

	tx, _ := base64.StdEncoding.DecodeString(fake.sentTx)
	msg := tx[1+64:]
	if !ed25519.Verify(ed25519.PublicKey(key.Identity().Bytes()), msg, tx[1:65]) {
		t.Fatal("the transaction is not signed by the slasher key")
	}
	want := anchorDiscriminator("slash")
	want = binary.LittleEndian.AppendUint64(want, 9)
	want = binary.LittleEndian.AppendUint64(want, 1_000)
	var reason [32]byte
	copy(reason[:], "MISSED_ROUND")
	want = append(want, reason[:]...)
	if !containsBytes(msg, want) {
		t.Fatal("instruction data is not discriminator, round, amount, left-aligned reason")
	}
	record := slasher.pda([]byte("slash"), binary.LittleEndian.AppendUint64(nil, 9), node.Identity().Bytes())
	if !containsBytes(msg, record[:]) {
		t.Fatal("the slash record for this round and node is not among the accounts")
	}
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

// A retry after a lost confirmation finds the slash record already there. That must read as done,
// the way AlreadySlashedForRound does on Arbitrum, or the executor would fail a penalty that landed.
func TestAnExistingSlashRecordIsReportedAsAlreadySlashed(t *testing.T) {
	fake, slasher, _ := slasherHarness(t)
	fake.simulationErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 0}}}
	fake.simulationLogs = []string{"Allocate: account Address { address: X, base: None } already in use"}

	node, _ := GenerateKeypair()
	_, err := slasher.Slash(context.Background(), Encode(node.Identity()), raw(1), raw(10), "OUTLIER_SUBMISSION")
	if !errors.Is(err, oracle.ErrAlreadySlashed) {
		t.Fatalf("err = %v, want oracle.ErrAlreadySlashed", err)
	}
	if fake.calls["sendTransaction"] != 0 {
		t.Fatal("a doomed slash was sent")
	}

	fake.simulationLogs = []string{"Program log: AnchorError occurred. Error Code: SlashExceedsCap."}
	_, err = slasher.Slash(context.Background(), Encode(node.Identity()), raw(1), raw(10), "OUTLIER_SUBMISSION")
	if err == nil || errors.Is(err, oracle.ErrAlreadySlashed) {
		t.Fatalf("a different refusal was reported as already slashed: %v", err)
	}
}

func TestSlashRefusesValuesTheProgramCannotTake(t *testing.T) {
	fake, slasher, _ := slasherHarness(t)
	node, _ := GenerateKeypair()
	addr := Encode(node.Identity())
	huge := pbtypes.NewRaw(new(big.Int).Lsh(big.NewInt(1), 64))

	for name, call := range map[string]func() error{
		"amount beyond u64": func() error { _, err := slasher.Slash(context.Background(), addr, raw(1), huge, "X"); return err },
		"reason over 32": func() error {
			_, err := slasher.Slash(context.Background(), addr, raw(1), raw(1), string(make([]byte, 33)))
			return err
		},
		"empty reason": func() error { _, err := slasher.Slash(context.Background(), addr, raw(1), raw(1), ""); return err },
		"bad node":     func() error { _, err := slasher.Slash(context.Background(), "0x1234", raw(1), raw(1), "X"); return err },
	} {
		if call() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if fake.calls["simulateTransaction"] != 0 {
		t.Fatal("an invalid slash reached the node")
	}
}
