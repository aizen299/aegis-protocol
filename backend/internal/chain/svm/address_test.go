package svm

import (
	"testing"

	"github.com/mr-tron/base58"
)

const vaultProgram = "h5VEwZjPpX6zug14r44i4QzpPzYBKeAHQYa5QZdzyGo"

// Vectors from `solana find-program-derived-address`.
func TestFindProgramAddressMatchesTheRuntime(t *testing.T) {
	program := MustIdentity(vaultProgram)

	if got := Encode(eventAuthority(program)); got != "8FUGbGRf4CSGZotCfE5krXSosJKaXPBVhLy8Lw78cQx6" {
		t.Errorf("event authority = %s", got)
	}

	usdc := MustIdentity("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")
	vault, _, err := FindProgramAddress([][]byte{[]byte("vault"), usdc[:]}, program)
	if err != nil {
		t.Fatal(err)
	}
	if got := Encode(vault); got != "FXM2L8ZApNEotY5fWr49zCRApbXDbsNgrJZUJMPbiQ2p" {
		t.Errorf("vault = %s", got)
	}
}

func TestDecodeAcceptsOnlyCanonical32ByteKeys(t *testing.T) {
	for _, bad := range []string{
		"",
		"0OIl",
		"11111111111111111111111111111111111",
		base58.Encode(make([]byte, 31)),
		base58.Encode(append([]byte{0}, MustIdentity(vaultProgram).Bytes()...)),
		"5VERv8NMvzbJMEkV8xnrLkEaWRtSz9CosKDYjCJjBRnbJLgp8uirBgmQpjKhoR4tjF3ZpRzrFmBV6UjKdiSZkQUW",
	} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("Decode(%q) accepted", bad)
		}
	}
	if _, err := Decode(vaultProgram); err != nil {
		t.Errorf("Decode(%s): %v", vaultProgram, err)
	}
}
