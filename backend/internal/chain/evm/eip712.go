package evm

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// EIP-712 signing for oracle submissions.
//
// This lives in the EVM package because the scheme is EVM-specific: an SVM node signs ed25519 over
// a different payload entirely. It is deliberately not part of chain.Client — that interface is
// read-only, and a write path shaped around secp256k1 would not survive the first non-EVM chain.
//
// Must stay in lockstep with OracleRounds.sol. The contract exposes `submissionDigest` so a node
// can cross-check a digest against the chain before trusting this implementation.

const (
	oracleDomainName    = "AegisOracle"
	oracleDomainVersion = "1"
)

var (
	eip712DomainTypehash = crypto.Keccak256Hash(
		[]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"),
	)
	submissionTypehash = crypto.Keccak256Hash(
		[]byte("Submission(uint256 roundId,bytes32 feedId,uint256 value,address node,uint256 nonce)"),
	)
)

// OracleSubmission is the payload a node signs.
type OracleSubmission struct {
	RoundID *big.Int
	FeedID  [32]byte
	Value   *big.Int
	Node    pbtypes.Identity
	Nonce   *big.Int
}

// SubmissionDigest computes the EIP-712 digest for a submission.
//
// chainID and verifyingContract come from the domain separator, so a signature cannot be replayed
// onto another chain or another deployment of the same contract.
func SubmissionDigest(chainID int64, verifyingContract pbtypes.Identity, sub OracleSubmission) ([32]byte, error) {
	var digest [32]byte

	contract, err := verifyingContract.EVMAddress()
	if err != nil {
		return digest, fmt.Errorf("verifying contract: %w", err)
	}
	node, err := sub.Node.EVMAddress()
	if err != nil {
		return digest, fmt.Errorf("node: %w", err)
	}

	domainSeparator := crypto.Keccak256(
		eip712DomainTypehash.Bytes(),
		crypto.Keccak256([]byte(oracleDomainName)),
		crypto.Keccak256([]byte(oracleDomainVersion)),
		padUint256(big.NewInt(chainID)),
		padAddress(contract),
	)

	structHash := crypto.Keccak256(
		submissionTypehash.Bytes(),
		padUint256(sub.RoundID),
		sub.FeedID[:],
		padUint256(sub.Value),
		padAddress(node),
		padUint256(sub.Nonce),
	)

	copy(digest[:], crypto.Keccak256([]byte{0x19, 0x01}, domainSeparator, structHash))
	return digest, nil
}

// SignSubmission produces a 65-byte signature the contract's ECDSA.recover accepts.
func SignSubmission(key NodeKey, chainID int64, verifyingContract pbtypes.Identity, sub OracleSubmission) ([]byte, error) {
	digest, err := SubmissionDigest(chainID, verifyingContract, sub)
	if err != nil {
		return nil, err
	}

	signature, err := crypto.Sign(digest[:], key.private)
	if err != nil {
		return nil, fmt.Errorf("sign submission: %w", err)
	}

	// go-ethereum returns v as 0/1; OpenZeppelin's ECDSA expects 27/28.
	signature[64] += 27
	return signature, nil
}

// IdentityFromKey derives a node's Identity from its signing key.
func IdentityFromKey(key *ecdsa.PrivateKey) pbtypes.Identity {
	return pbtypes.IdentityFromEVM(crypto.PubkeyToAddress(key.PublicKey))
}

// FeedID hashes a feed's human name into the on-chain identifier.
func FeedID(name string) [32]byte {
	return [32]byte(crypto.Keccak256Hash([]byte(name)))
}

func padUint256(n *big.Int) []byte {
	if n == nil {
		n = new(big.Int)
	}
	return common.LeftPadBytes(n.Bytes(), 32)
}

func padAddress(addr [20]byte) []byte {
	return common.LeftPadBytes(addr[:], 32)
}
