package evm

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// A vault's share offset is a property of our own contract, not a chain-generic concept the way a
// fungible token's decimals are. It therefore does not belong on chain.Client — an SVM
// implementation would have no counterpart to implement. It lives here as a separate reader that
// the indexer depends on through a narrow interface.
const vaultMetadataABI = `[
	{"name":"asset","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
	{"name":"virtualSharesOffset","type":"function","stateMutability":"pure","inputs":[],"outputs":[{"type":"uint8"}]}
]`

var vaultABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(vaultMetadataABI))
	if err != nil {
		panic("evm: vault metadata ABI is invalid: " + err.Error())
	}
	vaultABI = parsed
}

// VaultReader reads VaultEngine metadata over an existing chain client.
type VaultReader struct {
	client *Client
}

func NewVaultReader(client *Client) *VaultReader {
	return &VaultReader{client: client}
}

// VaultMetadata returns the vault's underlying asset and its virtual-shares offset.
//
// Both are required: without them a share amount has no scale, and the indexer would be storing a
// number nobody can interpret. A vault that does not answer is a hard failure rather than a
// default, on the same reasoning as token decimals.
func (r *VaultReader) VaultMetadata(ctx context.Context, vault pbtypes.Identity) (pbtypes.Identity, uint8, error) {
	raw, err := vault.EVMAddress()
	if err != nil {
		return pbtypes.Identity{}, 0, fmt.Errorf("vault %s: %w", vault.Hex(), err)
	}
	addr := common.Address(raw)

	assetValues, err := r.call(ctx, addr, "asset")
	if err != nil {
		return pbtypes.Identity{}, 0, fmt.Errorf("vault %s: asset(): %w", addr.Hex(), err)
	}
	asset, ok := assetValues[0].(common.Address)
	if !ok {
		return pbtypes.Identity{}, 0, fmt.Errorf("vault %s: asset() returned %T", addr.Hex(), assetValues[0])
	}

	offsetValues, err := r.call(ctx, addr, "virtualSharesOffset")
	if err != nil {
		return pbtypes.Identity{}, 0, fmt.Errorf("vault %s: virtualSharesOffset(): %w", addr.Hex(), err)
	}
	offset, ok := offsetValues[0].(uint8)
	if !ok {
		return pbtypes.Identity{}, 0, fmt.Errorf("vault %s: virtualSharesOffset() returned %T", addr.Hex(), offsetValues[0])
	}

	return pbtypes.IdentityFromEVM(asset), offset, nil
}

func (r *VaultReader) call(ctx context.Context, addr common.Address, method string) ([]any, error) {
	input, err := vaultABI.Pack(method)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", method, err)
	}

	out, err := r.client.rpc.CallContract(ctx, ethCallMsg(addr, input), nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s returned no data", method)
	}
	return vaultABI.Unpack(method, out)
}
