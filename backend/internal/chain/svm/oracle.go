package svm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"

	"github.com/aizen299/aegis-protocol/backend/internal/oraclenode"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

var (
	ed25519Program     = MustIdentity("Ed25519SigVerify111111111111111111111111111")
	instructionsSysvar = MustIdentity("Sysvar1nstructions1111111111111111111111111")
	systemProgram      = MustIdentity("11111111111111111111111111111111")
)

const (
	submissionDomain = "aegis-oracle-submission-v1"
	roundOpen        = 1
	roundQuorumMet   = 2
)

var maxU128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))

// SubmissionMessage is the byte string a node signs, in the layout the oracle program rebuilds and
// checks. A shared vector pins it on both sides. docs/v2.0-solana-plan.md §12.3.
func SubmissionMessage(program pbtypes.Identity, chainID int64, roundID uint64, feedID [32]byte, value *big.Int, node pbtypes.Identity, nonce uint64) ([]byte, error) {
	if value == nil || value.Sign() < 0 || value.Cmp(maxU128) > 0 {
		return nil, fmt.Errorf("value does not fit a u128")
	}
	out := make([]byte, 0, len(submissionDomain)+32+8+8+32+16+32+8)
	out = append(out, submissionDomain...)
	out = append(out, program[:]...)
	out = binary.LittleEndian.AppendUint64(out, uint64(chainID))
	out = binary.LittleEndian.AppendUint64(out, roundID)
	out = append(out, feedID[:]...)
	out = append(out, u128LE(value)...)
	out = append(out, node[:]...)
	out = binary.LittleEndian.AppendUint64(out, nonce)
	return out, nil
}

func u128LE(v *big.Int) []byte {
	be := v.FillBytes(make([]byte, 16))
	le := make([]byte, 16)
	for i := range be {
		le[15-i] = be[i]
	}
	return le
}

func anchorDiscriminator(name string) []byte {
	sum := sha256.Sum256([]byte("global:" + name))
	return sum[:8]
}

// ed25519Instruction is the verification Solana's own builder produces: one signature, with every
// offset reading from this instruction's data. The program refuses any other layout.
func ed25519Instruction(key Keypair, message []byte) Instruction {
	const pubkeyOffset = 16
	sigOffset := pubkeyOffset + 32
	msgOffset := sigOffset + 64
	data := []byte{1, 0}
	for _, v := range []int{sigOffset, 0xffff, pubkeyOffset, 0xffff, msgOffset, len(message), 0xffff} {
		data = binary.LittleEndian.AppendUint16(data, uint16(v))
	}
	id := key.Identity()
	data = append(data, id[:]...)
	data = append(data, key.Sign(message)...)
	data = append(data, message...)
	return Instruction{Program: ed25519Program, Data: data}
}

// OracleNodeChain is a node's view of the Solana oracle program: oraclenode.Chain for Solana.
type OracleNodeChain struct {
	client  *Client
	program pbtypes.Identity
	idl     *IDL
	key     Keypair
}

func NewOracleNodeChain(client *Client, key Keypair) (*OracleNodeChain, error) {
	for id, p := range client.programs {
		if _, ok := p.idl.accounts["Round"]; ok {
			return &OracleNodeChain{client: client, program: id, idl: p.idl, key: key}, nil
		}
	}
	return nil, errors.New("no oracle program is registered with the client")
}

func (c *OracleNodeChain) SignerAddress() string { return Encode(c.key.Identity()) }

func (c *OracleNodeChain) pda(seeds ...[]byte) pbtypes.Identity {
	id, _, err := FindProgramAddress(seeds, c.program)
	if err != nil {
		panic(err)
	}
	return id
}

// programAccount reads an account the program owns. A missing account is (nil, nil): for a node that
// never registered, or a round nobody submitted to yet, absence is the answer.
//
// A node reads at confirmed, not finalized: finalization trails by more than ten seconds, which a
// round of a minute or two cannot spare, and the worst a rolled-back read causes is a submission that
// fails in simulation. Indexing stays at finalized. §2.7.
func (c *OracleNodeChain) programAccount(ctx context.Context, name string, address pbtypes.Identity) (map[string]any, error) {
	owner, data, err := c.client.accountAt(ctx, address, "confirmed")
	if errors.Is(err, errAccountMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if owner != c.program {
		return nil, fmt.Errorf("%s %s is owned by %s, not the oracle program", name, Encode(address), Encode(owner))
	}
	return c.idl.DecodeAccount(name, data)
}

func feedIDFromHex(s string) ([32]byte, error) {
	var out [32]byte
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(s), "0x"))
	if err != nil || len(raw) != 32 {
		return out, fmt.Errorf("feed id %q is not 32 bytes of hex", s)
	}
	copy(out[:], raw)
	return out, nil
}

func bigField(fields map[string]any, name string) (*big.Int, error) {
	v, ok := fields[name].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("account field %s is %T", name, fields[name])
	}
	return v, nil
}

// CurrentRound asks the program's own questions, from its accounts: is the feed's round live, was
// this node in the set it froze, and has the node already submitted.
func (c *OracleNodeChain) CurrentRound(ctx context.Context, feedID string) (oraclenode.RoundView, error) {
	view := oraclenode.RoundView{FeedID: feedID}
	feed, err := feedIDFromHex(feedID)
	if err != nil {
		return view, err
	}

	feedAccount, err := c.programAccount(ctx, "Feed", c.pda([]byte("feed"), feed[:]))
	if err != nil || feedAccount == nil {
		return view, err
	}
	roundID, err := bigField(feedAccount, "currentRoundId")
	if err != nil || roundID.Sign() == 0 {
		return view, err
	}
	view.RoundID = pbtypes.NewRaw(roundID)
	view.Decimals, _ = feedAccount["decimals"].(uint8)

	roundAccount, err := c.programAccount(ctx, "Round", c.pda([]byte("round"), binary.LittleEndian.AppendUint64(nil, roundID.Uint64())))
	if err != nil {
		return view, err
	}
	if roundAccount == nil {
		return view, fmt.Errorf("feed names round %s, which has no account", roundID)
	}
	state, _ := roundAccount["state"].(uint8)
	deadline, err := bigField(roundAccount, "deadline")
	if err != nil {
		return view, err
	}
	view.Deadline = time.Unix(deadline.Int64(), 0)
	view.Open = state == roundOpen || state == roundQuorumMet
	if !view.Open {
		return view, nil
	}

	node := c.key.Identity()
	submission, err := c.programAccount(ctx, "Submission",
		c.pda([]byte("submission"), binary.LittleEndian.AppendUint64(nil, roundID.Uint64()), node[:]))
	if err != nil {
		return view, err
	}
	view.Submitted = submission != nil

	nodeAccount, err := c.programAccount(ctx, "Node", c.pda([]byte("node"), node[:]))
	if err != nil || nodeAccount == nil {
		return view, err
	}
	version, err := bigField(roundAccount, "nodeSetVersion")
	if err != nil {
		return view, err
	}
	activatedAt, err := bigField(nodeAccount, "activatedAtVersion")
	if err != nil {
		return view, err
	}
	active, _ := nodeAccount["active"].(bool)
	view.Eligible = active && activatedAt.Cmp(version) <= 0
	return view, nil
}

// Submit signs the attestation and sends it with the submission. The nonce is read immediately before
// signing: it is part of the signed bytes, and a stale one is refused.
func (c *OracleNodeChain) Submit(ctx context.Context, roundID pbtypes.Raw, feedID string, value pbtypes.Raw) (string, error) {
	feed, err := feedIDFromHex(feedID)
	if err != nil {
		return "", err
	}
	if !roundID.Big().IsUint64() {
		return "", fmt.Errorf("round id %s does not fit a u64", roundID)
	}
	round := roundID.Big().Uint64()
	node := c.key.Identity()

	config, err := c.programAccount(ctx, "Config", c.pda([]byte("config")))
	if err != nil || config == nil {
		return "", fmt.Errorf("read oracle config: %v", err)
	}
	// The program signs over its own chain id. A mismatch means this node points at a deployment for
	// another cluster, and every attestation it sent would be refused.
	programChain, err := bigField(config, "chainId")
	if err != nil {
		return "", err
	}
	if programChain.Int64() != c.client.chainID {
		return "", fmt.Errorf("oracle program is configured for chain %s, this client for %d", programChain, c.client.chainID)
	}

	nodeAccount, err := c.programAccount(ctx, "Node", c.pda([]byte("node"), node[:]))
	if err != nil || nodeAccount == nil {
		return "", fmt.Errorf("read node account: %v", err)
	}
	nonce, err := bigField(nodeAccount, "nonce")
	if err != nil {
		return "", err
	}

	message, err := SubmissionMessage(c.program, c.client.chainID, round, feed, value.Big(), node, nonce.Uint64())
	if err != nil {
		return "", err
	}

	data := anchorDiscriminator("submit")
	data = append(data, u128LE(value.Big())...)
	data = binary.LittleEndian.AppendUint64(data, nonce.Uint64())
	roundSeed := binary.LittleEndian.AppendUint64(nil, round)
	submit := Instruction{
		Program: c.program,
		Data:    data,
		Accounts: []AccountMeta{
			{Key: node, Signer: true, Writable: true},
			{Key: c.pda([]byte("config"))},
			{Key: c.pda([]byte("node"), node[:]), Writable: true},
			{Key: c.pda([]byte("round"), roundSeed), Writable: true},
			{Key: c.pda([]byte("submission"), roundSeed, node[:]), Writable: true},
			{Key: instructionsSysvar},
			{Key: systemProgram},
			{Key: c.pda(eventAuthoritySeed)},
			{Key: c.program},
		},
	}
	return c.client.Send(ctx, "confirmed", c.key, nil, ed25519Instruction(c.key, message), submit)
}

// FeedID is keccak256 of the feed's name, as on Arbitrum, so a feed has the same id on both chains.
func FeedID(name string) [32]byte {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(name))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
