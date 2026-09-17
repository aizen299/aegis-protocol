package evm

import (
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
)

// SignVAABody signs a Wormhole VAA body as a guardian does: over keccak256(keccak256(body)), as a
// 65-byte r ‖ s ‖ recovery id. Guardians are secp256k1 keys, so this lives behind the EVM boundary.
//
// Local tooling only: it plays the test guardian in the end-to-end suite. No production service signs
// VAAs.
func SignVAABody(key NodeKey, body []byte) ([]byte, error) {
	signature, err := crypto.Sign(crypto.Keccak256(crypto.Keccak256(body)), key.private)
	if err != nil {
		return nil, fmt.Errorf("sign vaa body: %w", err)
	}
	return signature, nil
}
