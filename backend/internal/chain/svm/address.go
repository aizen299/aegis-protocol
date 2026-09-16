// Package svm implements chain.Client against a Solana JSON-RPC endpoint.
//
// Everything it returns is chain-neutral: identities are 32-byte types.Identity, transaction ids are
// base58 signatures, and decoded amounts are *big.Int as on the EVM side.
package svm

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"filippo.io/edwards25519"
	"github.com/mr-tron/base58"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

var (
	TokenProgram       = MustIdentity("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA")
	errNoViableBump    = errors.New("no viable bump seed")
	errSeedTooLong     = errors.New("seed longer than 32 bytes")
	eventAuthoritySeed = []byte("__event_authority")
)

func Encode(id pbtypes.Identity) string {
	return base58.Encode(id[:])
}

// Decode accepts only the canonical 32-byte form: re-encoding must reproduce the input, so two
// spellings of one account can never become two rows.
func Decode(s string) (pbtypes.Identity, error) {
	raw, err := base58.Decode(s)
	if err != nil {
		return pbtypes.Identity{}, fmt.Errorf("decode base58 %q: %w", s, err)
	}
	id, err := pbtypes.IdentityFromBytes(raw)
	if err != nil {
		return pbtypes.Identity{}, fmt.Errorf("%q: %w", s, err)
	}
	if Encode(id) != s {
		return pbtypes.Identity{}, fmt.Errorf("%q is not the canonical encoding", s)
	}
	return id, nil
}

func MustIdentity(s string) pbtypes.Identity {
	id, err := Decode(s)
	if err != nil {
		panic(err)
	}
	return id
}

// FindProgramAddress mirrors the runtime's derivation: the first bump, counting down from 255, whose
// hash is not a valid ed25519 point.
func FindProgramAddress(seeds [][]byte, program pbtypes.Identity) (pbtypes.Identity, uint8, error) {
	for _, s := range seeds {
		if len(s) > 32 {
			return pbtypes.Identity{}, 0, errSeedTooLong
		}
	}
	for bump := 255; bump >= 0; bump-- {
		h := sha256.New()
		for _, s := range seeds {
			h.Write(s)
		}
		h.Write([]byte{byte(bump)})
		h.Write(program[:])
		h.Write([]byte("ProgramDerivedAddress"))
		sum := h.Sum(nil)
		if _, err := new(edwards25519.Point).SetBytes(sum); err != nil {
			id, _ := pbtypes.IdentityFromBytes(sum)
			return id, uint8(bump), nil
		}
	}
	return pbtypes.Identity{}, 0, errNoViableBump
}

func eventAuthority(program pbtypes.Identity) pbtypes.Identity {
	id, _, err := FindProgramAddress([][]byte{eventAuthoritySeed}, program)
	if err != nil {
		panic(fmt.Sprintf("event authority for %s: %v", Encode(program), err))
	}
	return id
}

// Codec is the Solana identity encoding with no RPC connection, for callers that only format and
// parse addresses.
type Codec struct{}

func (Codec) EncodeIdentity(id pbtypes.Identity) string { return Encode(id) }

func (Codec) DecodeIdentity(s string) (pbtypes.Identity, error) { return Decode(s) }
