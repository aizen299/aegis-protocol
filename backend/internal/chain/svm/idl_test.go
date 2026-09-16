package svm

import (
	"encoding/binary"
	"math/big"
	"strings"
	"testing"

	"github.com/aizen299/aegis-protocol/backend/pkg/contracts/solvault"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

func vaultIDL(t *testing.T) *IDL {
	t.Helper()
	idl, err := ParseIDL(solvault.IDL)
	if err != nil {
		t.Fatalf("parse committed idl: %v", err)
	}
	return idl
}

func depositedData(t *testing.T, idl *IDL, user, asset pbtypes.Identity, amount uint64, shares *big.Int) []byte {
	t.Helper()
	data := append([]byte{}, eventInstructionTag...)
	data = append(data, idl.events["Deposited"].discriminator...)
	data = append(data, user[:]...)
	data = append(data, asset[:]...)
	data = binary.LittleEndian.AppendUint64(data, amount)
	le := make([]byte, 16)
	be := shares.FillBytes(make([]byte, 16))
	for i := range be {
		le[15-i] = be[i]
	}
	return append(data, le...)
}

func TestTheCommittedIDLIsForTheVaultProgram(t *testing.T) {
	if got := Encode(vaultIDL(t).Program); got != vaultProgram {
		t.Fatalf("idl address = %s, want %s", got, vaultProgram)
	}
}

func TestDecodeDeposited(t *testing.T) {
	idl := vaultIDL(t)
	user, asset := MustIdentity(vaultProgram), TokenProgram
	shares, _ := new(big.Int).SetString("18446744073709551615000", 10)

	name, payload, err := idl.DecodeEvent(depositedData(t, idl, user, asset, 1_000_000, shares))
	if err != nil {
		t.Fatal(err)
	}
	if name != "Deposited" {
		t.Fatalf("name = %s", name)
	}
	if payload["user"] != user || payload["asset"] != asset {
		t.Errorf("identities = %v, %v", payload["user"], payload["asset"])
	}
	if payload["amount"].(*big.Int).Uint64() != 1_000_000 {
		t.Errorf("amount = %v", payload["amount"])
	}
	if payload["shares"].(*big.Int).Cmp(shares) != 0 {
		t.Errorf("shares = %v, want %v (a u128 wider than u64)", payload["shares"], shares)
	}
}

func TestMalformedEventDataIsRefused(t *testing.T) {
	idl := vaultIDL(t)
	good := depositedData(t, idl, TokenProgram, TokenProgram, 1, big.NewInt(1000))

	cases := map[string][]byte{
		"trailing byte":         append(append([]byte{}, good...), 0),
		"truncated":             good[:len(good)-1],
		"no event tag":          append([]byte{1}, good[1:]...),
		"unknown discriminator": append(append(append([]byte{}, good[:8]...), 0, 0, 0, 0, 0, 0, 0, 0), good[16:]...),
		"empty":                 nil,
	}
	for name, data := range cases {
		if _, _, err := idl.DecodeEvent(data); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

func TestEnumsDecodeByNameAndRefuseUnknownVariants(t *testing.T) {
	idl := vaultIDL(t)
	data := append(append([]byte{}, eventInstructionTag...), idl.events["RoleUpdated"].discriminator...)
	data = append(data, TokenProgram[:]...)
	data = append(data, 2)
	data = append(data, TokenProgram[:]...)

	_, payload, err := idl.DecodeEvent(data)
	if err != nil || payload["role"] != "Pauser" {
		t.Fatalf("role = %v, err = %v", payload["role"], err)
	}
	data[16+32] = 3
	if _, _, err := idl.DecodeEvent(data); err == nil {
		t.Error("role variant 3 decoded")
	}
}

// A type with no fixed size shifts every later field. It must fail when the IDL loads, not when the
// first event carrying it arrives.
func TestAnIDLWithUnsupportedTypesIsRefusedAtLoad(t *testing.T) {
	for _, ty := range []string{`"string"`, `{"vec":"u8"}`, `{"option":"pubkey"}`, `{"array":["u64",2]}`, `"f64"`} {
		raw := strings.Replace(string(solvault.IDL), `"name": "amount",
            "type": "u64"`, `"name": "amount",
            "type": `+ty, 1)
		if raw == string(solvault.IDL) {
			t.Fatal("test fixture did not find the field to replace")
		}
		if _, err := ParseIDL([]byte(raw)); err == nil {
			t.Errorf("type %s accepted", ty)
		}
	}
}
