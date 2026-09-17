package evm

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// The signature recovers the guardian's address from the double-keccak digest, which is what Wormhole's
// verifiers check, and not from a single hash of the body.
func TestVAABodySignatureRecoversTheGuardian(t *testing.T) {
	key, err := NodeKeyFromHex("cfb12303a19cde580bb4dd771639b0d26bc68353645571a8cff516ab2ee113a0")
	if err != nil {
		t.Fatal(err)
	}
	if key.Address() != "0xbefa429d57cd18b7f8a4d91a2da9ab4af05d0fbe" {
		t.Fatalf("development guardian address = %s", key.Address())
	}
	body := []byte("a vaa body")
	sig, err := SignVAABody(key, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 65 || sig[64] > 1 {
		t.Fatalf("signature is %d bytes with recovery id %d", len(sig), sig[len(sig)-1])
	}
	for name, digest := range map[string][]byte{
		"double keccak": crypto.Keccak256(crypto.Keccak256(body)),
		"single keccak": crypto.Keccak256(body),
	} {
		pub, err := crypto.SigToPub(digest, sig)
		recovered := err == nil && bytes.Equal(crypto.PubkeyToAddress(*pub).Bytes(), key.Identity[12:])
		if recovered != (name == "double keccak") {
			t.Errorf("%s: recovered the guardian = %v", name, recovered)
		}
	}
}
