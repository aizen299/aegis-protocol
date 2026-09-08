package types

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
)

// Identity is a chain-agnostic account or contract identifier.
//
// It is 32 bytes because a Solana pubkey is 32 bytes; an EVM address is left-padded into the low
// 20. Never narrow this to an EVM address in shared types — see docs/project-spec.md §7.
//
// An Identity is only meaningful alongside its chain ID: the byte layout is shared, the canonical
// string encoding is not (hex on EVM, base58 on Solana).
type Identity [32]byte

var (
	ErrNotEVMAddress   = errors.New("identity does not fit in an EVM address")
	ErrBadIdentitySize = errors.New("identity must be 32 bytes")
)

// IdentityFromEVM left-pads a 20-byte EVM address into an Identity.
func IdentityFromEVM(addr [20]byte) Identity {
	var id Identity
	copy(id[12:], addr[:])
	return id
}

// IdentityFromBytes builds an Identity from an exactly-32-byte slice.
func IdentityFromBytes(b []byte) (Identity, error) {
	var id Identity
	if len(b) != 32 {
		return id, fmt.Errorf("%w: got %d", ErrBadIdentitySize, len(b))
	}
	copy(id[:], b)
	return id, nil
}

// IdentityFromEVMHex parses a 0x-prefixed 20-byte hex address.
func IdentityFromEVMHex(s string) (Identity, error) {
	var id Identity
	trimmed := s
	if len(trimmed) >= 2 && (trimmed[:2] == "0x" || trimmed[:2] == "0X") {
		trimmed = trimmed[2:]
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return id, fmt.Errorf("decode evm address %q: %w", s, err)
	}
	if len(raw) != 20 {
		return id, fmt.Errorf("%w: %q is %d bytes", ErrNotEVMAddress, s, len(raw))
	}
	copy(id[12:], raw)
	return id, nil
}

// EVMAddress narrows the Identity to 20 bytes, erroring if the high 12 bytes are set.
func (i Identity) EVMAddress() ([20]byte, error) {
	var addr [20]byte
	if !bytes.Equal(i[:12], make([]byte, 12)) {
		return addr, ErrNotEVMAddress
	}
	copy(addr[:], i[12:])
	return addr, nil
}

// EVMHex renders the low 20 bytes as a 0x-prefixed hex string. Valid only for EVM chains; use the
// chain client's encoder when the chain is not known statically.
func (i Identity) EVMHex() string {
	return "0x" + hex.EncodeToString(i[12:])
}

// Hex renders all 32 bytes. Chain-neutral, used for logging and debugging, not for storage.
func (i Identity) Hex() string {
	return "0x" + hex.EncodeToString(i[:])
}

func (i Identity) IsZero() bool {
	return i == Identity{}
}

func (i Identity) Bytes() []byte {
	return i[:]
}
