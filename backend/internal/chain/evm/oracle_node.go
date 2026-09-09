package evm

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/aizen299/aegis-protocol/backend/internal/oraclenode"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const oracleNodeABI = `[
	{"name":"currentRoundId","type":"function","stateMutability":"view",
	 "inputs":[{"type":"bytes32"}],"outputs":[{"type":"uint256"}]},
	{"name":"feedDecimals","type":"function","stateMutability":"view",
	 "inputs":[{"type":"bytes32"}],"outputs":[{"type":"uint8"}]},
	{"name":"nonceOf","type":"function","stateMutability":"view",
	 "inputs":[{"type":"address"}],"outputs":[{"type":"uint256"}]},
	{"name":"hasSubmitted","type":"function","stateMutability":"view",
	 "inputs":[{"type":"uint256"},{"type":"address"}],"outputs":[{"type":"bool"}]},
	{"name":"roundOf","type":"function","stateMutability":"view","inputs":[{"type":"uint256"}],
	 "outputs":[{"type":"tuple","components":[
		{"name":"feedId","type":"bytes32"},{"name":"openedAt","type":"uint64"},
		{"name":"deadline","type":"uint64"},{"name":"settledAt","type":"uint64"},
		{"name":"state","type":"uint8"},{"name":"nodeSetVersion","type":"uint256"},
		{"name":"eligibleCount","type":"uint256"},{"name":"aggregatedValue","type":"uint256"}]}]},
	{"name":"submit","type":"function","stateMutability":"nonpayable",
	 "inputs":[{"type":"uint256"},{"type":"uint256"},{"type":"uint256"},{"type":"bytes"}],"outputs":[]}
]`

const stakingEligibilityABI = `[
	{"name":"isEligibleAt","type":"function","stateMutability":"view",
	 "inputs":[{"type":"address"},{"type":"uint256"}],"outputs":[{"type":"bool"}]}
]`

var (
	nodeRoundsABI  abi.ABI
	nodeStakingABI abi.ABI
)

func init() {
	rounds, err := abi.JSON(strings.NewReader(oracleNodeABI))
	if err != nil {
		panic("evm: oracle node ABI is invalid: " + err.Error())
	}
	nodeRoundsABI = rounds

	staking, err := abi.JSON(strings.NewReader(stakingEligibilityABI))
	if err != nil {
		panic("evm: staking eligibility ABI is invalid: " + err.Error())
	}
	nodeStakingABI = staking
}

// roundState mirrors IOracleRounds.RoundState. OPEN and QUORUM_MET both still accept submissions.
const (
	roundStateOpen      = 1
	roundStateQuorumMet = 2
)

// OracleNodeChain is the node's view of the oracle contracts.
type OracleNodeChain struct {
	client   *Client
	rounds   common.Address
	staking  common.Address
	key      NodeKey
	chainID  *big.Int
	gasLimit uint64
}

func NewOracleNodeChain(client *Client, rounds, staking pbtypes.Identity, key NodeKey, gasLimit uint64) (*OracleNodeChain, error) {
	roundsAddr, err := rounds.EVMAddress()
	if err != nil {
		return nil, fmt.Errorf("rounds address: %w", err)
	}
	stakingAddr, err := staking.EVMAddress()
	if err != nil {
		return nil, fmt.Errorf("staking address: %w", err)
	}
	if gasLimit == 0 {
		gasLimit = 400_000
	}

	return &OracleNodeChain{
		client:   client,
		rounds:   common.Address(roundsAddr),
		staking:  common.Address(stakingAddr),
		key:      key,
		chainID:  big.NewInt(client.ChainID()),
		gasLimit: gasLimit,
	}, nil
}

func (c *OracleNodeChain) SignerAddress() string { return c.key.Address() }

// CurrentRound reports whether there is a round this node should submit to.
//
// Eligibility is checked against the version the round froze at open, not the live set — the same
// question the contract will ask, so the node does not pay gas to be told no.
func (c *OracleNodeChain) CurrentRound(ctx context.Context, feedID string) (oraclenode.RoundView, error) {
	var view oraclenode.RoundView

	feed, err := feedIDFromHex(feedID)
	if err != nil {
		return view, err
	}
	view.FeedID = feedID

	roundID, err := c.callBig(ctx, c.rounds, nodeRoundsABI, "currentRoundId", feed)
	if err != nil {
		return view, fmt.Errorf("current round: %w", err)
	}
	if roundID.Sign() == 0 {
		return view, nil
	}
	view.RoundID = pbtypes.NewRaw(roundID)

	decimals, err := c.callUint8Raw(ctx, c.rounds, nodeRoundsABI, "feedDecimals", feed)
	if err != nil {
		return view, fmt.Errorf("feed decimals: %w", err)
	}
	view.Decimals = decimals

	round, err := c.callRound(ctx, roundID)
	if err != nil {
		return view, err
	}
	view.Open = round.State == roundStateOpen || round.State == roundStateQuorumMet
	view.Deadline = time.Unix(int64(round.Deadline), 0)
	if !view.Open {
		return view, nil
	}

	signer := common.HexToAddress(c.key.Address())

	submitted, err := c.callBool(ctx, c.rounds, nodeRoundsABI, "hasSubmitted", roundID, signer)
	if err != nil {
		return view, fmt.Errorf("has submitted: %w", err)
	}
	view.Submitted = submitted

	eligible, err := c.callBool(ctx, c.staking, nodeStakingABI, "isEligibleAt", signer, round.NodeSetVersion)
	if err != nil {
		return view, fmt.Errorf("eligibility: %w", err)
	}
	view.Eligible = eligible

	return view, nil
}

// Submit signs the value and sends it. The nonce is read immediately before signing, because it is
// part of the digest and a stale one produces a signature the contract rejects.
func (c *OracleNodeChain) Submit(ctx context.Context, roundID pbtypes.Raw, feedID string, value pbtypes.Raw) (string, error) {
	feed, err := feedIDFromHex(feedID)
	if err != nil {
		return "", err
	}
	signer := common.HexToAddress(c.key.Address())

	nonce, err := c.callBig(ctx, c.rounds, nodeRoundsABI, "nonceOf", signer)
	if err != nil {
		return "", fmt.Errorf("submission nonce: %w", err)
	}

	signature, err := SignSubmission(c.key, c.chainID.Int64(), pbtypes.IdentityFromEVM(signer), OracleSubmission{
		RoundID: roundID.Big(),
		FeedID:  feed,
		Value:   value.Big(),
		Node:    c.key.Identity,
		Nonce:   nonce,
	})
	if err != nil {
		return "", err
	}

	input, err := nodeRoundsABI.Pack("submit", roundID.Big(), value.Big(), nonce, signature)
	if err != nil {
		return "", fmt.Errorf("pack submit: %w", err)
	}

	// Simulated first: a reverting submission costs gas and, worse, looks like a missed round.
	if _, err := c.client.rpc.CallContract(ctx, ethereum.CallMsg{
		From: signer, To: &c.rounds, Data: input,
	}, nil); err != nil {
		return "", fmt.Errorf("submission would revert: %w", err)
	}

	txNonce, err := c.client.rpc.PendingNonceAt(ctx, signer)
	if err != nil {
		return "", fmt.Errorf("account nonce: %w", err)
	}
	gasPrice, err := c.client.rpc.SuggestGasPrice(ctx)
	if err != nil {
		return "", fmt.Errorf("gas price: %w", err)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    txNonce,
		To:       &c.rounds,
		Value:    big.NewInt(0),
		Gas:      c.gasLimit,
		GasPrice: gasPrice,
		Data:     input,
	})
	signed, err := types.SignTx(tx, types.NewEIP155Signer(c.chainID), c.key.private)
	if err != nil {
		return "", fmt.Errorf("sign submission tx: %w", err)
	}
	if err := c.client.rpc.SendTransaction(ctx, signed); err != nil {
		return "", fmt.Errorf("send submission: %w", err)
	}

	return signed.Hash().Hex(), nil
}

type onChainRound struct {
	FeedID          [32]byte
	OpenedAt        uint64
	Deadline        uint64
	SettledAt       uint64
	State           uint8
	NodeSetVersion  *big.Int
	EligibleCount   *big.Int
	AggregatedValue *big.Int
}

func (c *OracleNodeChain) callRound(ctx context.Context, roundID *big.Int) (onChainRound, error) {
	var round onChainRound

	values, err := c.call(ctx, c.rounds, nodeRoundsABI, "roundOf", roundID)
	if err != nil {
		return round, fmt.Errorf("round: %w", err)
	}
	if err := nodeRoundsABI.Methods["roundOf"].Outputs.Copy(&round, values); err != nil {
		return round, fmt.Errorf("decode round: %w", err)
	}
	return round, nil
}

func (c *OracleNodeChain) call(ctx context.Context, to common.Address, contractABI abi.ABI, method string, args ...any) ([]any, error) {
	input, err := contractABI.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", method, err)
	}

	out, err := c.client.rpc.CallContract(ctx, ethereum.CallMsg{To: &to, Data: input}, nil)
	if err != nil {
		return nil, err
	}
	return contractABI.Unpack(method, out)
}

func (c *OracleNodeChain) callBig(ctx context.Context, to common.Address, contractABI abi.ABI, method string, args ...any) (*big.Int, error) {
	values, err := c.call(ctx, to, contractABI, method, args...)
	if err != nil {
		return nil, err
	}
	v, ok := values[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("%s returned %T, want uint256", method, values[0])
	}
	return v, nil
}

func (c *OracleNodeChain) callBool(ctx context.Context, to common.Address, contractABI abi.ABI, method string, args ...any) (bool, error) {
	values, err := c.call(ctx, to, contractABI, method, args...)
	if err != nil {
		return false, err
	}
	v, ok := values[0].(bool)
	if !ok {
		return false, fmt.Errorf("%s returned %T, want bool", method, values[0])
	}
	return v, nil
}

func (c *OracleNodeChain) callUint8Raw(ctx context.Context, to common.Address, contractABI abi.ABI, method string, args ...any) (uint8, error) {
	values, err := c.call(ctx, to, contractABI, method, args...)
	if err != nil {
		return 0, err
	}
	v, ok := values[0].(uint8)
	if !ok {
		return 0, fmt.Errorf("%s returned %T, want uint8", method, values[0])
	}
	return v, nil
}
