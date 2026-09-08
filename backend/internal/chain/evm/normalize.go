package evm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// normalizeValue converts go-ethereum ABI values into the chain-neutral types the rest of the
// backend consumes. Addresses become types.Identity here so that nothing downstream of this
// package handles a 20-byte EVM address.
func normalizeValue(v any) any {
	switch typed := v.(type) {
	case common.Address:
		return types.IdentityFromEVM(typed)
	case common.Hash:
		return [32]byte(typed)
	case []common.Address:
		out := make([]types.Identity, len(typed))
		for i, a := range typed {
			out[i] = types.IdentityFromEVM(a)
		}
		return out
	case *big.Int:
		return new(big.Int).Set(typed)
	default:
		return v
	}
}

func normalizePayload(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = normalizeValue(v)
	}
	return out
}
