package evm

import (
	"math/big"
	"testing"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

func testSubmission(t *testing.T, node pbtypes.Identity) OracleSubmission {
	t.Helper()
	return OracleSubmission{
		RoundID: big.NewInt(7),
		FeedID:  FeedID("ETH/USD"),
		Value:   big.NewInt(3000),
		Node:    node,
		Nonce:   big.NewInt(0),
	}
}

func testContract(t *testing.T) pbtypes.Identity {
	t.Helper()
	id, err := pbtypes.IdentityFromEVMHex("0x610178da211fef7d417bc0e6fed39f05609ad788")
	if err != nil {
		t.Fatalf("contract address: %v", err)
	}
	return id
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	key, err := GenerateNodeKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	contract := testContract(t)
	sub := testSubmission(t, key.Identity)

	signature, err := SignSubmission(key, 31337, contract, sub)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	ok, err := VerifySubmission(31337, contract, sub, signature)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("a signature this package produced did not verify")
	}
}

// The digest binds every field. Changing any one of them must invalidate the signature, or a
// submission could be replayed into a different round, feed, or value.
func TestVerificationIsBoundToEveryField(t *testing.T) {
	key, _ := GenerateNodeKey()
	contract := testContract(t)
	sub := testSubmission(t, key.Identity)

	signature, err := SignSubmission(key, 31337, contract, sub)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	mutations := map[string]func(OracleSubmission) OracleSubmission{
		"round": func(s OracleSubmission) OracleSubmission { s.RoundID = big.NewInt(8); return s },
		"feed":  func(s OracleSubmission) OracleSubmission { s.FeedID = FeedID("BTC/USD"); return s },
		"value": func(s OracleSubmission) OracleSubmission { s.Value = big.NewInt(9000); return s },
		"nonce": func(s OracleSubmission) OracleSubmission { s.Nonce = big.NewInt(1); return s },
	}

	for name, mutate := range mutations {
		ok, err := VerifySubmission(31337, contract, mutate(sub), signature)
		if err != nil {
			t.Fatalf("%s: verify errored: %v", name, err)
		}
		if ok {
			t.Errorf("a changed %s still verified; the digest does not bind it", name)
		}
	}
}

// The domain separator carries the chain id and the contract address, so a signature cannot be
// replayed onto another chain or another deployment.
func TestVerificationIsBoundToChainAndContract(t *testing.T) {
	key, _ := GenerateNodeKey()
	contract := testContract(t)
	sub := testSubmission(t, key.Identity)

	signature, _ := SignSubmission(key, 31337, contract, sub)

	if ok, _ := VerifySubmission(42161, contract, sub, signature); ok {
		t.Error("a signature verified on a different chain id")
	}

	other, _ := pbtypes.IdentityFromEVMHex("0x0000000000000000000000000000000000000009")
	if ok, _ := VerifySubmission(31337, other, sub, signature); ok {
		t.Error("a signature verified against a different contract")
	}
}

// A signature from one node presented as another's must not verify.
func TestVerificationRejectsAnotherNodesSignature(t *testing.T) {
	signer, _ := GenerateNodeKey()
	impostor, _ := GenerateNodeKey()
	contract := testContract(t)

	signature, _ := SignSubmission(signer, 31337, contract, testSubmission(t, signer.Identity))

	ok, err := VerifySubmission(31337, contract, testSubmission(t, impostor.Identity), signature)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("one node's signature verified as another's")
	}
}

func TestVerificationRejectsMalformedSignature(t *testing.T) {
	key, _ := GenerateNodeKey()
	contract := testContract(t)
	sub := testSubmission(t, key.Identity)

	for name, signature := range map[string][]byte{
		"empty":     {},
		"truncated": make([]byte, 64),
		"oversized": make([]byte, 66),
	} {
		if _, err := VerifySubmission(31337, contract, sub, signature); err == nil {
			t.Errorf("%s signature did not error", name)
		}
	}
}

// The stored signature carries v as 27/28 because that is what the contract's ECDSA.recover wants.
// Verification must normalise rather than reject it.
func TestVerificationAcceptsContractStyleRecoveryID(t *testing.T) {
	key, _ := GenerateNodeKey()
	contract := testContract(t)
	sub := testSubmission(t, key.Identity)

	signature, _ := SignSubmission(key, 31337, contract, sub)
	if signature[64] != 27 && signature[64] != 28 {
		t.Fatalf("signature v = %d, want the contract form", signature[64])
	}

	ok, err := VerifySubmission(31337, contract, sub, signature)
	if err != nil || !ok {
		t.Fatalf("verify: ok=%v err=%v", ok, err)
	}
}
