// Package evm implements chain.Client against an EVM JSON-RPC endpoint.
//
// This is the only package in the backend permitted to import ethclient or handle 20-byte
// addresses on the read path. Everything it returns is chain-neutral.
package evm

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Client reads events from an EVM chain and decodes them with the ABIs registered at construction.
type Client struct {
	rpc        *ethclient.Client
	chainID    int64
	confirms   uint64
	abis       map[common.Address]abi.ABI
	eventNames map[common.Address]map[common.Hash]string
}

// Registration binds a contract address to the ABI used to decode its logs.
type Registration struct {
	Address pbtypes.Identity
	ABI     abi.ABI
}

// Options configures a Client.
type Options struct {
	RPCURL            string
	ChainID           int64
	ConfirmationDepth uint64
	Contracts         []Registration
}

// New dials the endpoint and verifies the reported chain ID matches the configured one. A mismatch
// is fatal: indexing the wrong chain into chain-scoped tables corrupts them silently.
func New(ctx context.Context, opts Options) (*Client, error) {
	rpc, err := ethclient.DialContext(ctx, opts.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", opts.RPCURL, err)
	}

	reported, err := rpc.ChainID(ctx)
	if err != nil {
		rpc.Close()
		return nil, fmt.Errorf("fetch chain id: %w", err)
	}
	if reported.Int64() != opts.ChainID {
		rpc.Close()
		return nil, fmt.Errorf("chain id mismatch: configured %d, endpoint reports %s", opts.ChainID, reported)
	}

	c := &Client{
		rpc:        rpc,
		chainID:    opts.ChainID,
		confirms:   opts.ConfirmationDepth,
		abis:       make(map[common.Address]abi.ABI, len(opts.Contracts)),
		eventNames: make(map[common.Address]map[common.Hash]string, len(opts.Contracts)),
	}

	for _, reg := range opts.Contracts {
		addr, err := reg.Address.EVMAddress()
		if err != nil {
			rpc.Close()
			return nil, fmt.Errorf("contract %s: %w", reg.Address.Hex(), err)
		}
		evmAddr := common.Address(addr)
		c.abis[evmAddr] = reg.ABI

		topics := make(map[common.Hash]string, len(reg.ABI.Events))
		for name, ev := range reg.ABI.Events {
			topics[ev.ID] = name
		}
		c.eventNames[evmAddr] = topics
	}

	return c, nil
}

func (c *Client) ChainID() int64 { return c.chainID }

func (c *Client) ConfirmationDepth() uint64 { return c.confirms }

func (c *Client) Close() { c.rpc.Close() }

func (c *Client) Head(ctx context.Context) (uint64, error) {
	head, err := c.rpc.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("block number: %w", err)
	}
	return head, nil
}

func (c *Client) EncodeIdentity(id pbtypes.Identity) string {
	return id.EVMHex()
}

func (c *Client) DecodeIdentity(s string) (pbtypes.Identity, error) {
	return pbtypes.IdentityFromEVMHex(s)
}

func (c *Client) LogsInRange(ctx context.Context, from, to uint64, filters []chain.Filter) ([]chain.Event, error) {
	if len(filters) == 0 {
		return nil, nil
	}

	addresses, wanted, err := c.buildQuery(filters)
	if err != nil {
		return nil, err
	}

	logs, err := c.rpc.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: addresses,
	})
	if err != nil {
		return nil, fmt.Errorf("filter logs [%d,%d]: %w", from, to, err)
	}

	blockTimes := make(map[uint64]int64)
	events := make([]chain.Event, 0, len(logs))

	for i := range logs {
		lg := logs[i]
		if lg.Removed {
			continue
		}

		decoded, ok, err := c.decode(lg)
		if err != nil {
			return nil, fmt.Errorf("decode log %s#%d: %w", lg.TxHash.Hex(), lg.Index, err)
		}
		if !ok {
			continue
		}
		if names, restricted := wanted[lg.Address]; restricted && !names[decoded.Name] {
			continue
		}

		ts, err := c.blockTime(ctx, lg.BlockNumber, blockTimes)
		if err != nil {
			return nil, err
		}
		decoded.BlockTime = ts

		events = append(events, decoded)
	}

	return events, nil
}

func (c *Client) buildQuery(filters []chain.Filter) ([]common.Address, map[common.Address]map[string]bool, error) {
	addresses := make([]common.Address, 0, len(filters))
	wanted := make(map[common.Address]map[string]bool, len(filters))

	for _, f := range filters {
		raw, err := f.Contract.EVMAddress()
		if err != nil {
			return nil, nil, fmt.Errorf("filter contract %s: %w", f.Contract.Hex(), err)
		}
		addr := common.Address(raw)
		addresses = append(addresses, addr)

		if len(f.Names) == 0 {
			continue
		}
		names, ok := wanted[addr]
		if !ok {
			names = make(map[string]bool, len(f.Names))
			wanted[addr] = names
		}
		for _, n := range f.Names {
			names[n] = true
		}
	}

	return addresses, wanted, nil
}

// decode returns ok=false for logs the client has no ABI for, which is not an error: a contract
// emits events the indexer does not consume.
func (c *Client) decode(lg types.Log) (chain.Event, bool, error) {
	var out chain.Event

	contractABI, ok := c.abis[lg.Address]
	if !ok || len(lg.Topics) == 0 {
		return out, false, nil
	}
	name, ok := c.eventNames[lg.Address][lg.Topics[0]]
	if !ok {
		return out, false, nil
	}
	event, ok := contractABI.Events[name]
	if !ok {
		return out, false, nil
	}

	payload := make(map[string]any)
	if len(lg.Data) > 0 {
		if err := contractABI.UnpackIntoMap(payload, name, lg.Data); err != nil {
			return out, false, fmt.Errorf("unpack data: %w", err)
		}
	}

	indexed := indexedArgs(event.Inputs)
	if len(indexed) > 0 {
		if len(lg.Topics) < len(indexed)+1 {
			return out, false, fmt.Errorf("event %s: want %d topics, got %d", name, len(indexed)+1, len(lg.Topics))
		}
		if err := abi.ParseTopicsIntoMap(payload, indexed, lg.Topics[1:]); err != nil {
			return out, false, fmt.Errorf("parse topics: %w", err)
		}
	}

	return chain.Event{
		ChainID:     c.chainID,
		BlockNumber: lg.BlockNumber,
		TxHash:      [32]byte(lg.TxHash),
		LogIndex:    lg.Index,
		Contract:    pbtypes.IdentityFromEVM(lg.Address),
		Name:        name,
		Payload:     normalizePayload(payload),
	}, true, nil
}

func (c *Client) blockTime(ctx context.Context, number uint64, cache map[uint64]int64) (int64, error) {
	if ts, ok := cache[number]; ok {
		return ts, nil
	}
	header, err := c.rpc.HeaderByNumber(ctx, new(big.Int).SetUint64(number))
	if err != nil {
		return 0, fmt.Errorf("header %d: %w", number, err)
	}
	ts := int64(header.Time)
	cache[number] = ts
	return ts, nil
}

func indexedArgs(inputs abi.Arguments) abi.Arguments {
	out := make(abi.Arguments, 0, len(inputs))
	for _, in := range inputs {
		if in.Indexed {
			out = append(out, in)
		}
	}
	return out
}

// TxHashHex renders a transaction hash in the canonical EVM form used in Postgres.
func TxHashHex(h [32]byte) string {
	return "0x" + strings.ToLower(hex.EncodeToString(h[:]))
}
