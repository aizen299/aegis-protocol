package evm

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// NodeKey is an EVM signing identity.
//
// Key material is chain-specific — secp256k1 here, ed25519 on Solana — so generating and handling
// it lives behind this boundary alongside the signer. Callers outside internal/chain/evm hold a
// NodeKey and never a *ecdsa.PrivateKey.
type NodeKey struct {
	private  *ecdsa.PrivateKey
	Identity pbtypes.Identity
}

// GenerateNodeKey creates a fresh signing identity.
func GenerateNodeKey() (NodeKey, error) {
	key, err := crypto.GenerateKey()
	if err != nil {
		return NodeKey{}, fmt.Errorf("generate node key: %w", err)
	}
	return NodeKey{private: key, Identity: IdentityFromKey(key)}, nil
}

// NodeKeyFromHex parses a 0x-prefixed or bare 32-byte hex private key.
func NodeKeyFromHex(s string) (NodeKey, error) {
	trimmed := s
	if len(trimmed) >= 2 && (trimmed[:2] == "0x" || trimmed[:2] == "0X") {
		trimmed = trimmed[2:]
	}

	key, err := crypto.HexToECDSA(trimmed)
	if err != nil {
		return NodeKey{}, fmt.Errorf("parse node key: %w", err)
	}
	return NodeKey{private: key, Identity: IdentityFromKey(key)}, nil
}

// Address renders the key's canonical lowercase EVM address.
func (k NodeKey) Address() string { return k.Identity.EVMHex() }

// Hex returns the 0x-prefixed private key.
//
// Only for local tooling that shells out to a signer it does not control, such as `cast send
// --private-key` in the end-to-end tests. Production signing goes through SignSubmission, which
// never exposes the key.
func (k NodeKey) Hex() string {
	return "0x" + hex.EncodeToString(crypto.FromECDSA(k.private))
}
