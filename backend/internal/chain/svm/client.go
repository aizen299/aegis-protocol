package svm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/mr-tron/base58"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	signaturePageLimit = 1000
	signatureLen       = 64
	mintAccountLen     = 82
	mintDecimalsOffset = 44
	mintInitialized    = 45
)

// genesisHashes pins each public cluster to its genesis. A local validator makes a new genesis on
// every start, so solana-localnet is deliberately absent: it is the one cluster that cannot be pinned.
var genesisHashes = map[int64]string{
	pbtypes.ChainIDSolanaMainnet: "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d",
	pbtypes.ChainIDSolanaDevnet:  "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG",
}

var errAccountMissing = errors.New("account does not exist")

type Registration struct {
	IDL *IDL
}

type Options struct {
	RPCURL     string
	ChainID    int64
	Programs   []Registration
	HTTPClient *http.Client
}

type program struct {
	idl            *IDL
	eventAuthority pbtypes.Identity
}

// Client reads Solana programs' events. One Client serves one cluster.
type Client struct {
	rpc      *rpcClient
	chainID  int64
	programs map[pbtypes.Identity]program
}

func New(ctx context.Context, opts Options) (*Client, error) {
	c, err := newClient(opts)
	if err != nil {
		return nil, err
	}
	if want, pinned := genesisHashes[opts.ChainID]; pinned {
		var got string
		if err := c.rpc.call(ctx, "getGenesisHash", nil, &got); err != nil {
			return nil, err
		}
		if got != want {
			return nil, fmt.Errorf("chain id %d expects genesis %s, endpoint reports %s", opts.ChainID, want, got)
		}
	}
	return c, nil
}

func newClient(opts Options) (*Client, error) {
	info, ok := pbtypes.LookupChain(opts.ChainID)
	if !ok || info.VM != pbtypes.VMSVM {
		return nil, fmt.Errorf("chain id %d is not a known Solana cluster", opts.ChainID)
	}
	c := &Client{
		rpc:      newRPC(opts.RPCURL, opts.HTTPClient),
		chainID:  opts.ChainID,
		programs: make(map[pbtypes.Identity]program, len(opts.Programs)),
	}
	for _, reg := range opts.Programs {
		if reg.IDL == nil {
			return nil, fmt.Errorf("program registration without an idl")
		}
		c.programs[reg.IDL.Program] = program{idl: reg.IDL, eventAuthority: eventAuthority(reg.IDL.Program)}
	}
	return c, nil
}

func (c *Client) ChainID() int64 { return c.chainID }

// ConfirmationDepth is zero because Head is already the finalized slot.
func (c *Client) ConfirmationDepth() uint64 { return 0 }

func (c *Client) Close() {}

func (c *Client) EncodeIdentity(id pbtypes.Identity) string { return Encode(id) }

func (c *Client) DecodeIdentity(s string) (pbtypes.Identity, error) { return Decode(s) }

func (c *Client) Head(ctx context.Context) (uint64, error) {
	var slot uint64
	err := c.rpc.call(ctx, "getSlot", []any{map[string]any{"commitment": commitment}}, &slot)
	return slot, err
}

func (c *Client) BlockTime(ctx context.Context, slot uint64) (int64, error) {
	var t *int64
	if err := c.rpc.call(ctx, "getBlockTime", []any{slot}, &t); err != nil {
		return 0, err
	}
	if t == nil {
		return 0, fmt.Errorf("slot %d has no block time", slot)
	}
	return *t, nil
}

// LogsInRange returns the events registered programs emitted in finalized, successful transactions
// in slots [from, to]. See docs/v2.0-solana-plan.md §10.2 and §10.3.
func (c *Client) LogsInRange(ctx context.Context, from, to uint64, filters []chain.Filter) ([]chain.Event, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	if from > to {
		return nil, fmt.Errorf("range [%d,%d] is empty", from, to)
	}

	wanted := make(map[pbtypes.Identity]map[string]bool)
	for _, f := range filters {
		if _, ok := c.programs[f.Contract]; !ok {
			return nil, fmt.Errorf("program %s has no registered idl", Encode(f.Contract))
		}
		names := wanted[f.Contract]
		if names == nil {
			names = make(map[string]bool)
			wanted[f.Contract] = names
		}
		if len(f.Names) == 0 {
			names["*"] = true
		}
		for _, n := range f.Names {
			names[n] = true
		}
	}

	var firstAvailable uint64
	if err := c.rpc.call(ctx, "getFirstAvailableBlock", nil, &firstAvailable); err != nil {
		return nil, err
	}
	// A node that has pruned the range answers with no signatures, which is indistinguishable from
	// no activity. Refuse rather than record the gap as empty.
	if from < firstAvailable {
		return nil, fmt.Errorf("slot %d is older than the node's first available block %d", from, firstAvailable)
	}

	var signatures []signatureInfo
	seen := make(map[string]bool)
	for contract := range wanted {
		found, err := c.signaturesInRange(ctx, contract, from, to)
		if err != nil {
			return nil, err
		}
		for _, s := range found {
			if !seen[s.Signature] {
				seen[s.Signature] = true
				signatures = append(signatures, s)
			}
		}
	}
	// Newest first from the node; oldest first for handlers. Ties within a slot keep the node's order.
	slices.SortStableFunc(signatures, func(a, b signatureInfo) int {
		switch {
		case a.Slot < b.Slot:
			return -1
		case a.Slot > b.Slot:
			return 1
		}
		return 0
	})

	var events []chain.Event
	for _, s := range signatures {
		txEvents, err := c.transactionEvents(ctx, s.Signature, wanted)
		if err != nil {
			return nil, err
		}
		events = append(events, txEvents...)
	}
	return events, nil
}

func (c *Client) signaturesInRange(ctx context.Context, address pbtypes.Identity, from, to uint64) ([]signatureInfo, error) {
	var out []signatureInfo
	before := ""
	for {
		cfg := map[string]any{"commitment": commitment, "limit": signaturePageLimit}
		if before != "" {
			cfg["before"] = before
		}
		var page []signatureInfo
		if err := c.rpc.call(ctx, "getSignaturesForAddress", []any{Encode(address), cfg}, &page); err != nil {
			return nil, err
		}
		for _, s := range page {
			if s.Slot < from {
				return out, nil
			}
			if s.Slot <= to && !failed(s.Err) {
				out = append(out, s)
			}
		}
		if len(page) < signaturePageLimit {
			return out, nil
		}
		before = page[len(page)-1].Signature
	}
}

func (c *Client) transactionEvents(ctx context.Context, signature string, wanted map[pbtypes.Identity]map[string]bool) ([]chain.Event, error) {
	raw, err := base58.Decode(signature)
	if err != nil || len(raw) != signatureLen {
		return nil, fmt.Errorf("signature %q is not a 64-byte base58 value", signature)
	}

	var tx *transaction
	cfg := map[string]any{"commitment": commitment, "encoding": "json", "maxSupportedTransactionVersion": 0}
	if err := c.rpc.call(ctx, "getTransaction", []any{signature, cfg}, &tx); err != nil {
		return nil, err
	}
	if tx == nil || tx.Meta == nil {
		return nil, fmt.Errorf("transaction %s: not found at %s commitment", signature, commitment)
	}
	// Inner instructions of a failed transaction can still be reported, for the calls that ran
	// before it failed. None of them happened.
	if failed(tx.Meta.Err) {
		return nil, nil
	}
	if tx.BlockTime == nil {
		return nil, fmt.Errorf("transaction %s: no block time", signature)
	}

	keys := append([]string{}, tx.Transaction.Message.AccountKeys...)
	if la := tx.Meta.LoadedAddresses; la != nil {
		keys = append(keys, la.Writable...)
		keys = append(keys, la.Readonly...)
	}
	key := func(i int) (pbtypes.Identity, error) {
		if i < 0 || i >= len(keys) {
			return pbtypes.Identity{}, fmt.Errorf("account index %d out of range", i)
		}
		return Decode(keys[i])
	}

	groups := tx.Meta.InnerInstructions
	slices.SortStableFunc(groups, func(a, b struct {
		Index        int                   `json:"index"`
		Instructions []compiledInstruction `json:"instructions"`
	}) int {
		return a.Index - b.Index
	})

	var events []chain.Event
	logIndex := uint(0)
	// Only inner instructions: an event is a CPI from the program to itself, never a top-level call.
	for _, group := range groups {
		for _, ix := range group.Instructions {
			programID, err := key(ix.ProgramIDIndex)
			if err != nil {
				return nil, fmt.Errorf("transaction %s: %w", signature, err)
			}
			prog, registered := c.programs[programID]
			if !registered {
				continue
			}
			data, err := base58.Decode(ix.Data)
			if err != nil {
				return nil, fmt.Errorf("transaction %s: instruction data: %w", signature, err)
			}
			if len(data) < discriminatorLen || string(data[:discriminatorLen]) != string(eventInstructionTag) {
				continue
			}
			if len(ix.Accounts) == 0 {
				return nil, fmt.Errorf("transaction %s: event instruction has no accounts", signature)
			}
			authority, err := key(ix.Accounts[0])
			if err != nil {
				return nil, fmt.Errorf("transaction %s: %w", signature, err)
			}
			if authority != prog.eventAuthority {
				return nil, fmt.Errorf("transaction %s: event data for %s not signed by its event authority", signature, Encode(programID))
			}

			name, payload, err := prog.idl.DecodeEvent(data)
			if err != nil {
				return nil, fmt.Errorf("transaction %s: %w", signature, err)
			}
			index := logIndex
			logIndex++

			names := wanted[programID]
			if names == nil || !(names["*"] || names[name]) {
				continue
			}
			events = append(events, chain.Event{
				ChainID:     c.chainID,
				BlockNumber: tx.Slot,
				BlockTime:   *tx.BlockTime,
				TxHash:      signature,
				LogIndex:    index,
				Contract:    programID,
				Name:        name,
				Payload:     payload,
			})
		}
	}
	return events, nil
}

func (c *Client) account(ctx context.Context, address pbtypes.Identity) (pbtypes.Identity, []byte, error) {
	return c.accountAt(ctx, address, commitment)
}

func (c *Client) accountAt(ctx context.Context, address pbtypes.Identity, level string) (owner pbtypes.Identity, data []byte, err error) {
	var result struct {
		Value *accountInfo `json:"value"`
	}
	cfg := map[string]any{"commitment": level, "encoding": "base64"}
	if err := c.rpc.call(ctx, "getAccountInfo", []any{Encode(address), cfg}, &result); err != nil {
		return owner, nil, err
	}
	if result.Value == nil {
		return owner, nil, fmt.Errorf("account %s: %w", Encode(address), errAccountMissing)
	}
	if len(result.Value.Data) != 2 || result.Value.Data[1] != "base64" {
		return owner, nil, fmt.Errorf("account %s: unexpected data encoding", Encode(address))
	}
	data, err = base64.StdEncoding.DecodeString(result.Value.Data[0])
	if err != nil {
		return owner, nil, fmt.Errorf("account %s: %w", Encode(address), err)
	}
	owner, err = Decode(result.Value.Owner)
	return owner, data, err
}

// TokenMetadata reads a classic SPL mint's decimals. Name and symbol live in an optional metadata
// program and are left empty. §10.6.
func (c *Client) TokenMetadata(ctx context.Context, token pbtypes.Identity) (chain.TokenMeta, error) {
	owner, data, err := c.account(ctx, token)
	if err != nil {
		return chain.TokenMeta{}, err
	}
	if owner != TokenProgram {
		return chain.TokenMeta{}, fmt.Errorf("mint %s is owned by %s, not the SPL Token program", Encode(token), Encode(owner))
	}
	if len(data) != mintAccountLen || data[mintInitialized] != 1 {
		return chain.TokenMeta{}, fmt.Errorf("account %s is not an initialized mint", Encode(token))
	}
	return chain.TokenMeta{Decimals: data[mintDecimalsOffset]}, nil
}

// VaultLocator finds the vault the Solana vault program holds for an asset. §10.4.
type VaultLocator struct {
	client *Client
}

func NewVaultLocator(client *Client) *VaultLocator {
	return &VaultLocator{client: client}
}

func (l *VaultLocator) LocateVault(ctx context.Context, emitter, asset pbtypes.Identity) (pbtypes.Identity, pbtypes.Identity, uint8, error) {
	prog, ok := l.client.programs[emitter]
	if !ok {
		return pbtypes.Identity{}, pbtypes.Identity{}, 0, fmt.Errorf("program %s has no registered idl", Encode(emitter))
	}
	vault, _, err := FindProgramAddress([][]byte{[]byte("vault"), asset[:]}, emitter)
	if err != nil {
		return pbtypes.Identity{}, pbtypes.Identity{}, 0, err
	}
	owner, data, err := l.client.account(ctx, vault)
	if err != nil {
		return pbtypes.Identity{}, pbtypes.Identity{}, 0, err
	}
	if owner != emitter {
		return pbtypes.Identity{}, pbtypes.Identity{}, 0, fmt.Errorf("vault %s is owned by %s, not the program", Encode(vault), Encode(owner))
	}
	fields, err := prog.idl.DecodeAccount("Vault", data)
	if err != nil {
		return pbtypes.Identity{}, pbtypes.Identity{}, 0, fmt.Errorf("vault %s: %w", Encode(vault), err)
	}
	mint, ok := fields["mint"].(pbtypes.Identity)
	if !ok {
		return pbtypes.Identity{}, pbtypes.Identity{}, 0, fmt.Errorf("vault %s: no mint field", Encode(vault))
	}
	offset, ok := fields["shareOffset"].(uint8)
	if !ok {
		return pbtypes.Identity{}, pbtypes.Identity{}, 0, fmt.Errorf("vault %s: no shareOffset field", Encode(vault))
	}
	return vault, mint, offset, nil
}
