package types

import (
	"testing"
)

func TestIdentityFromEVMHexRoundTrip(t *testing.T) {
	const addr = "0x5fbdb2315678afecb367f032d93f642f64180aa3"

	id, err := IdentityFromEVMHex(addr)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := id.EVMHex(); got != addr {
		t.Fatalf("round trip: got %s, want %s", got, addr)
	}
	for i := range 12 {
		if id[i] != 0 {
			t.Fatalf("byte %d should be zero padding, got %x", i, id[i])
		}
	}
}

func TestIdentityFromEVMHexRejectsWrongLength(t *testing.T) {
	for _, in := range []string{"0xdeadbeef", "0x" + string(make([]byte, 0)), "not-hex"} {
		if _, err := IdentityFromEVMHex(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}

func TestEVMAddressRejectsWideIdentity(t *testing.T) {
	var id Identity
	id[0] = 0x01

	if _, err := id.EVMAddress(); err == nil {
		t.Fatal("a 32-byte identity must not narrow silently to an EVM address")
	}
}

func TestIdentityFromBytesRequires32(t *testing.T) {
	if _, err := IdentityFromBytes(make([]byte, 20)); err == nil {
		t.Fatal("expected error for a 20-byte input")
	}
	if _, err := IdentityFromBytes(make([]byte, 32)); err != nil {
		t.Fatalf("32 bytes should parse: %v", err)
	}
}

func TestIsEVMChain(t *testing.T) {
	cases := map[int64]bool{
		ChainIDArbitrumOne:    true,
		ChainIDAnvil:          true,
		NonEVMChainIDBase:     false,
		NonEVMChainIDBase + 1: false,
		0:                     false,
		-1:                    false,
	}
	for id, want := range cases {
		if got := IsEVMChain(id); got != want {
			t.Errorf("IsEVMChain(%d) = %v, want %v", id, got, want)
		}
	}
}

// The canonical stored form is lowercase. Checksummed input must normalise to it, or a deploy
// artifact's address will not match the same address read back from Postgres.
func TestEVMHexIsCanonicalLowercase(t *testing.T) {
	const checksummed = "0xA513E6E4b8f2a923D98304ec87F64353C4D5C853"
	const canonical = "0xa513e6e4b8f2a923d98304ec87f64353c4d5c853"

	id, err := IdentityFromEVMHex(checksummed)
	if err != nil {
		t.Fatalf("checksummed input must parse: %v", err)
	}
	if got := id.EVMHex(); got != canonical {
		t.Fatalf("got %s, want %s", got, canonical)
	}

	lower, err := IdentityFromEVMHex(canonical)
	if err != nil {
		t.Fatalf("lowercase input must parse: %v", err)
	}
	if lower != id {
		t.Fatal("checksummed and lowercase forms of one address must produce the same Identity")
	}
}
