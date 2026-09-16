package svm

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Keypair is a Solana signing identity. The private key never leaves this package: callers hold a
// Keypair and ask it for its identity, and signing happens only inside transaction building.
type Keypair struct {
	private ed25519.PrivateKey
}

// KeypairFromJSON parses solana-keygen's format, a JSON array of 64 bytes: the seed, then the public
// key. The two halves must agree, so a truncated or hand-edited key fails rather than signing as
// someone unexpected. §12.8.
func KeypairFromJSON(s string) (Keypair, error) {
	var raw []int
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &raw); err != nil {
		return Keypair{}, errors.New("solana key is not a solana-keygen JSON array")
	}
	if len(raw) != ed25519.PrivateKeySize {
		return Keypair{}, fmt.Errorf("solana key has %d bytes, want %d", len(raw), ed25519.PrivateKeySize)
	}
	bytes := make([]byte, len(raw))
	for i, v := range raw {
		if v < 0 || v > 255 {
			return Keypair{}, errors.New("solana key contains a value outside a byte")
		}
		bytes[i] = byte(v)
	}
	derived := ed25519.NewKeyFromSeed(bytes[:ed25519.SeedSize])
	if !derived.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(bytes[ed25519.SeedSize:])) {
		return Keypair{}, errors.New("solana key's public half does not match its seed")
	}
	return Keypair{private: derived}, nil
}

func GenerateKeypair() (Keypair, error) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return Keypair{}, err
	}
	return Keypair{private: priv}, nil
}

func (k Keypair) Identity() pbtypes.Identity {
	id, _ := pbtypes.IdentityFromBytes(k.private.Public().(ed25519.PublicKey))
	return id
}

func (k Keypair) Sign(message []byte) []byte {
	return ed25519.Sign(k.private, message)
}

type AccountMeta struct {
	Key      pbtypes.Identity
	Signer   bool
	Writable bool
}

type Instruction struct {
	Program  pbtypes.Identity
	Accounts []AccountMeta
	Data     []byte
}

func compactU16(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n == 0 {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

// BuildTransaction serialises and signs a legacy transaction. Accounts are merged, then ordered as
// the runtime requires: fee payer, signer-writable, signer-readonly, writable, readonly.
func BuildTransaction(blockhash pbtypes.Identity, payer Keypair, signers []Keypair, ixs []Instruction) ([]byte, error) {
	type entry struct {
		AccountMeta
		order int
	}
	payerID := payer.Identity()
	merged := map[pbtypes.Identity]*entry{payerID: {AccountMeta{payerID, true, true}, 0}}
	add := func(m AccountMeta) {
		if e, ok := merged[m.Key]; ok {
			e.Signer = e.Signer || m.Signer
			e.Writable = e.Writable || m.Writable
			return
		}
		merged[m.Key] = &entry{m, len(merged)}
	}
	for _, ix := range ixs {
		for _, a := range ix.Accounts {
			add(a)
		}
		add(AccountMeta{Key: ix.Program})
	}
	if len(merged) > 256 {
		return nil, fmt.Errorf("transaction references %d accounts, the limit is 256", len(merged))
	}

	entries := make([]*entry, 0, len(merged))
	for _, e := range merged {
		entries = append(entries, e)
	}
	rank := func(e *entry) int {
		switch {
		case e.Key == payerID:
			return 0
		case e.Signer && e.Writable:
			return 1
		case e.Signer:
			return 2
		case e.Writable:
			return 3
		}
		return 4
	}
	sort.Slice(entries, func(i, j int) bool {
		if ri, rj := rank(entries[i]), rank(entries[j]); ri != rj {
			return ri < rj
		}
		return entries[i].order < entries[j].order
	})

	index := make(map[pbtypes.Identity]int, len(entries))
	var numSigners, readonlySigned, readonlyUnsigned int
	for i, e := range entries {
		index[e.Key] = i
		switch {
		case e.Signer:
			numSigners++
			if !e.Writable {
				readonlySigned++
			}
		case !e.Writable:
			readonlyUnsigned++
		}
	}

	msg := []byte{byte(numSigners), byte(readonlySigned), byte(readonlyUnsigned)}
	msg = append(msg, compactU16(len(entries))...)
	for _, e := range entries {
		msg = append(msg, e.Key[:]...)
	}
	msg = append(msg, blockhash[:]...)
	msg = append(msg, compactU16(len(ixs))...)
	for _, ix := range ixs {
		msg = append(msg, byte(index[ix.Program]))
		msg = append(msg, compactU16(len(ix.Accounts))...)
		for _, a := range ix.Accounts {
			msg = append(msg, byte(index[a.Key]))
		}
		msg = append(msg, compactU16(len(ix.Data))...)
		msg = append(msg, ix.Data...)
	}

	keys := map[pbtypes.Identity]Keypair{payerID: payer}
	for _, s := range signers {
		keys[s.Identity()] = s
	}
	tx := compactU16(numSigners)
	for _, e := range entries[:numSigners] {
		k, ok := keys[e.Key]
		if !ok {
			return nil, fmt.Errorf("no key for signer %s", Encode(e.Key))
		}
		tx = append(tx, k.Sign(msg)...)
	}
	return append(tx, msg...), nil
}

// ErrTransactionFailed carries the program logs of a transaction the node rejected in simulation or
// that landed and failed, so a caller can tell one refusal from another.
type ErrTransactionFailed struct {
	Signature string
	Message   string
	Logs      []string
}

func (e *ErrTransactionFailed) Error() string {
	return fmt.Sprintf("transaction failed: %s", e.Message)
}

// LogsContain reports whether any log line contains s. Anchor writes a refusal's error name to the
// logs, which is how a specific one is recognised.
func (e *ErrTransactionFailed) LogsContain(s string) bool {
	for _, l := range e.Logs {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// Send simulates, submits, and waits for the transaction to reach `commitment` ("confirmed" or
// "finalized"). Simulation runs first because a failing submission still costs a fee.
func (c *Client) Send(ctx context.Context, commitment string, payer Keypair, signers []Keypair, ixs ...Instruction) (string, error) {
	var blockhash struct {
		Value struct {
			Blockhash string `json:"blockhash"`
		} `json:"value"`
	}
	if err := c.rpc.call(ctx, "getLatestBlockhash", []any{map[string]any{"commitment": "confirmed"}}, &blockhash); err != nil {
		return "", err
	}
	recent, err := Decode(blockhash.Value.Blockhash)
	if err != nil {
		return "", fmt.Errorf("blockhash: %w", err)
	}
	raw, err := BuildTransaction(recent, payer, signers, ixs)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(raw)

	var simulation struct {
		Value struct {
			Err  json.RawMessage `json:"err"`
			Logs []string        `json:"logs"`
		} `json:"value"`
	}
	simCfg := map[string]any{"encoding": "base64", "commitment": "confirmed", "sigVerify": true}
	if err := c.rpc.call(ctx, "simulateTransaction", []any{encoded, simCfg}, &simulation); err != nil {
		return "", err
	}
	if failed(simulation.Value.Err) {
		return "", &ErrTransactionFailed{Message: string(simulation.Value.Err), Logs: simulation.Value.Logs}
	}

	var signature string
	sendCfg := map[string]any{"encoding": "base64", "skipPreflight": true}
	if err := c.rpc.call(ctx, "sendTransaction", []any{encoded, sendCfg}, &signature); err != nil {
		return "", err
	}
	return signature, c.waitFor(ctx, signature, commitment)
}

func (c *Client) waitFor(ctx context.Context, signature, commitment string) error {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		var statuses struct {
			Value []*struct {
				Err                json.RawMessage `json:"err"`
				ConfirmationStatus string          `json:"confirmationStatus"`
			} `json:"value"`
		}
		cfg := map[string]any{"searchTransactionHistory": true}
		if err := c.rpc.call(ctx, "getSignatureStatuses", []any{[]string{signature}, cfg}, &statuses); err != nil {
			return err
		}
		if len(statuses.Value) == 1 && statuses.Value[0] != nil {
			s := statuses.Value[0]
			reached := s.ConfirmationStatus == "finalized" || (commitment == "confirmed" && s.ConfirmationStatus == "confirmed")
			if reached {
				if failed(s.Err) {
					return &ErrTransactionFailed{Signature: signature, Message: string(s.Err)}
				}
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", signature, ctx.Err())
		case <-ticker.C:
		}
	}
}
