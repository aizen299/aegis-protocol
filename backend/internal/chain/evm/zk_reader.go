package evm

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// A gate's tree and verifier are properties of our own contract, so this lives here as a separate
// reader rather than on chain.Client, for the same reason the vault and governor readers do.
const zkGateMetadataABI = `[
	{"name":"tree","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
	{"name":"verifier","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]}
]`

var zkGateABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(zkGateMetadataABI))
	if err != nil {
		panic("evm: zk gate metadata ABI is invalid: " + err.Error())
	}
	zkGateABI = parsed
}

// ZkReader reads ZkVaultGate metadata over an existing chain client.
type ZkReader struct {
	client *Client
}

func NewZkReader(client *Client) *ZkReader {
	return &ZkReader{client: client}
}

// ZkGateMetadata returns the gate's commitment tree and verifier.
//
// Both are required. A gate that does not answer is a hard failure rather than a default: the tree
// address determines which commitment set a proof is checked against, and guessing it wrong means
// indexing a different tree than the one the contract uses.
func (r *ZkReader) ZkGateMetadata(ctx context.Context, gate pbtypes.Identity) (pbtypes.Identity, pbtypes.Identity, error) {
	raw, err := gate.EVMAddress()
	if err != nil {
		return pbtypes.Identity{}, pbtypes.Identity{}, fmt.Errorf("gate %s: %w", gate.Hex(), err)
	}
	addr := common.Address(raw)

	tree, err := r.address(ctx, addr, "tree")
	if err != nil {
		return pbtypes.Identity{}, pbtypes.Identity{}, err
	}
	verifier, err := r.address(ctx, addr, "verifier")
	if err != nil {
		return pbtypes.Identity{}, pbtypes.Identity{}, err
	}

	return pbtypes.IdentityFromEVM(tree), pbtypes.IdentityFromEVM(verifier), nil
}

func (r *ZkReader) address(ctx context.Context, gate common.Address, method string) (common.Address, error) {
	input, err := zkGateABI.Pack(method)
	if err != nil {
		return common.Address{}, fmt.Errorf("pack %s: %w", method, err)
	}

	out, err := r.client.rpc.CallContract(ctx, ethCallMsg(gate, input), nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("gate %s: %s(): %w", gate.Hex(), method, err)
	}
	if len(out) == 0 {
		return common.Address{}, fmt.Errorf("gate %s: %s() returned no data", gate.Hex(), method)
	}

	values, err := zkGateABI.Unpack(method, out)
	if err != nil {
		return common.Address{}, fmt.Errorf("gate %s: unpack %s: %w", gate.Hex(), method, err)
	}
	value, ok := values[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("gate %s: %s() returned %T", gate.Hex(), method, values[0])
	}
	return value, nil
}
