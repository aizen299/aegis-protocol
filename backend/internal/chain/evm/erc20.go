package evm

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const erc20MetadataABI = `[
	{"name":"decimals","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"uint8"}]},
	{"name":"symbol","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"string"}]},
	{"name":"name","type":"function","stateMutability":"view","inputs":[],"outputs":[{"type":"string"}]}
]`

var erc20ABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(erc20MetadataABI))
	if err != nil {
		panic("evm: erc20 metadata ABI is invalid: " + err.Error())
	}
	erc20ABI = parsed
}

// TokenMetadata reads decimals, symbol, and name from an ERC-20.
//
// decimals() is required: an amount stored without it cannot be interpreted, so a token that does
// not implement it is refused rather than defaulted. symbol() and name() are optional — plenty of
// real tokens return bytes32 or omit them, and neither affects correctness.
func (c *Client) TokenMetadata(ctx context.Context, token pbtypes.Identity) (chain.TokenMeta, error) {
	var meta chain.TokenMeta

	raw, err := token.EVMAddress()
	if err != nil {
		return meta, fmt.Errorf("token %s: %w", token.Hex(), err)
	}
	addr := common.Address(raw)

	decimals, err := c.callUint8(ctx, addr, "decimals")
	if err != nil {
		return meta, fmt.Errorf("token %s: decimals(): %w", addr.Hex(), err)
	}
	meta.Decimals = decimals

	if symbol, err := c.callString(ctx, addr, "symbol"); err == nil {
		meta.Symbol = symbol
	}
	if name, err := c.callString(ctx, addr, "name"); err == nil {
		meta.Name = name
	}

	return meta, nil
}

func (c *Client) call(ctx context.Context, addr common.Address, method string) ([]any, error) {
	input, err := erc20ABI.Pack(method)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", method, err)
	}

	out, err := c.rpc.CallContract(ctx, ethCallMsg(addr, input), nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s returned no data", method)
	}

	return erc20ABI.Unpack(method, out)
}

// ethCallMsg builds a plain read-only call. Shared so every contract reader in this package issues
// calls the same way.
func ethCallMsg(to common.Address, data []byte) ethereum.CallMsg {
	return ethereum.CallMsg{To: &to, Data: data}
}

func (c *Client) callUint8(ctx context.Context, addr common.Address, method string) (uint8, error) {
	values, err := c.call(ctx, addr, method)
	if err != nil {
		return 0, err
	}
	v, ok := values[0].(uint8)
	if !ok {
		return 0, fmt.Errorf("%s returned %T, want uint8", method, values[0])
	}
	return v, nil
}

func (c *Client) callString(ctx context.Context, addr common.Address, method string) (string, error) {
	values, err := c.call(ctx, addr, method)
	if err != nil {
		return "", err
	}
	v, ok := values[0].(string)
	if !ok {
		return "", fmt.Errorf("%s returned %T, want string", method, values[0])
	}
	return v, nil
}
