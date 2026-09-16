package svm

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// SubmissionVerifier checks a stored attestation off chain, independently of the program that
// accepted it: oracle.SignatureVerifier for Solana.
type SubmissionVerifier struct {
	program pbtypes.Identity
}

func NewSubmissionVerifier(program pbtypes.Identity) *SubmissionVerifier {
	return &SubmissionVerifier{program: program}
}

func (v *SubmissionVerifier) Verify(chainID int64, roundID pbtypes.Raw, feedID string, value pbtypes.Raw, node string, nonce pbtypes.Raw, signature []byte) (bool, error) {
	nodeID, err := Decode(node)
	if err != nil {
		return false, fmt.Errorf("node %q: %w", node, err)
	}
	feed, err := feedIDFromHex(feedID)
	if err != nil {
		return false, err
	}
	if !roundID.Big().IsUint64() || !nonce.Big().IsUint64() {
		return false, fmt.Errorf("round %s or nonce %s does not fit a u64", roundID, nonce)
	}
	message, err := SubmissionMessage(v.program, chainID, roundID.Big().Uint64(), feed, value.Big(), nodeID, nonce.Big().Uint64())
	if err != nil {
		return false, err
	}
	// A malformed signature is an invalid one, not an error: it is exactly what the check exists to
	// find, and an error would retry the round forever instead of recording the verdict.
	if len(signature) != ed25519.SignatureSize {
		return false, nil
	}
	return ed25519.Verify(ed25519.PublicKey(nodeID[:]), message, signature), nil
}

// Slasher submits penalties with the program's slasher key: oracle.Slasher for Solana.
type Slasher struct {
	client  *Client
	program pbtypes.Identity
	key     Keypair
}

func NewSlasher(client *Client, program pbtypes.Identity, key Keypair) (*Slasher, error) {
	if _, ok := client.programs[program]; !ok {
		return nil, errors.New("the oracle program is not registered with the client")
	}
	return &Slasher{client: client, program: program, key: key}, nil
}

func (s *Slasher) SignerAddress() string { return Encode(s.key.Identity()) }

func (s *Slasher) pda(seeds ...[]byte) pbtypes.Identity {
	id, _, err := FindProgramAddress(seeds, s.program)
	if err != nil {
		panic(err)
	}
	return id
}

// Slash sends one penalty. The slash record the program creates makes a second penalty for the same
// round fail in simulation, which is reported as oracle.ErrAlreadySlashed so a retry after a lost
// confirmation closes the decision instead of failing it. §12.8.
func (s *Slasher) Slash(ctx context.Context, node string, roundID, amount pbtypes.Raw, reason string) (string, error) {
	nodeID, err := Decode(node)
	if err != nil {
		return "", fmt.Errorf("node %q: %w", node, err)
	}
	if !roundID.Big().IsUint64() || !amount.Big().IsUint64() {
		return "", fmt.Errorf("round %s or amount %s does not fit a u64", roundID, amount)
	}
	if len(reason) == 0 || len(reason) > 32 {
		return "", fmt.Errorf("reason %q must be 1 to 32 bytes", reason)
	}
	round := roundID.Big().Uint64()
	var reasonBytes [32]byte
	copy(reasonBytes[:], reason)

	data := anchorDiscriminator("slash")
	data = binary.LittleEndian.AppendUint64(data, round)
	data = binary.LittleEndian.AppendUint64(data, amount.Big().Uint64())
	data = append(data, reasonBytes[:]...)

	slasher := s.key.Identity()
	ix := Instruction{
		Program: s.program,
		Data:    data,
		Accounts: []AccountMeta{
			{Key: slasher, Signer: true, Writable: true},
			{Key: s.pda([]byte("config")), Writable: true},
			{Key: s.pda([]byte("node"), nodeID[:]), Writable: true},
			{Key: s.pda([]byte("slash"), binary.LittleEndian.AppendUint64(nil, round), nodeID[:]), Writable: true},
			{Key: systemProgram},
			{Key: s.pda(eventAuthoritySeed)},
			{Key: s.program},
		},
	}

	signature, err := s.client.Send(ctx, "confirmed", s.key, nil, ix)
	var failed *ErrTransactionFailed
	if errors.As(err, &failed) && failed.Signature == "" && failed.LogsContain("already in use") {
		// Only the slash record is created by this instruction, so an account already in use can
		// only be that record: this node has taken its penalty for this round.
		return "", oracle.ErrAlreadySlashed
	}
	return signature, err
}
