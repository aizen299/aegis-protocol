package evm

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// A governor's vote token and timelock are properties of our own contract, so they live here as a
// separate reader rather than on chain.Client, for the same reason the vault's share offset does.
const governorMetadataABI = `[
	{"name":"token","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]},
	{"name":"timelock","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"address"}]}
]`

var governorABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(governorMetadataABI))
	if err != nil {
		panic("evm: governor metadata ABI is invalid: " + err.Error())
	}
	governorABI = parsed
}

// GovernanceReader reads Governor metadata over an existing chain client.
type GovernanceReader struct {
	client *Client
}

func NewGovernanceReader(client *Client) *GovernanceReader {
	return &GovernanceReader{client: client}
}

// GovernorMetadata returns the governor's vote token and its timelock.
//
// The token is required, not optional: it carries the decimals every vote weight is denominated in,
// and a weight stored without its scale is a number nobody can interpret. A governor that does not
// answer is a hard failure rather than a default.
func (r *GovernanceReader) GovernorMetadata(ctx context.Context, governor pbtypes.Identity) (pbtypes.Identity, pbtypes.Identity, error) {
	raw, err := governor.EVMAddress()
	if err != nil {
		return pbtypes.Identity{}, pbtypes.Identity{}, fmt.Errorf("governor %s: %w", governor.Hex(), err)
	}
	addr := common.Address(raw)

	token, err := r.address(ctx, addr, "token")
	if err != nil {
		return pbtypes.Identity{}, pbtypes.Identity{}, err
	}
	timelock, err := r.address(ctx, addr, "timelock")
	if err != nil {
		return pbtypes.Identity{}, pbtypes.Identity{}, err
	}

	return pbtypes.IdentityFromEVM(token), pbtypes.IdentityFromEVM(timelock), nil
}

func (r *GovernanceReader) address(ctx context.Context, governor common.Address, method string) (common.Address, error) {
	input, err := governorABI.Pack(method)
	if err != nil {
		return common.Address{}, fmt.Errorf("pack %s: %w", method, err)
	}

	out, err := r.client.rpc.CallContract(ctx, ethCallMsg(governor, input), nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("governor %s: %s(): %w", governor.Hex(), method, err)
	}
	if len(out) == 0 {
		return common.Address{}, fmt.Errorf("governor %s: %s() returned no data", governor.Hex(), method)
	}

	values, err := governorABI.Unpack(method, out)
	if err != nil {
		return common.Address{}, fmt.Errorf("governor %s: unpack %s: %w", governor.Hex(), method, err)
	}
	value, ok := values[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("governor %s: %s() returned %T", governor.Hex(), method, values[0])
	}
	return value, nil
}
