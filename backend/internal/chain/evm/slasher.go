package evm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const slasherABI = `[
	{"name":"slash","type":"function","stateMutability":"nonpayable",
	 "inputs":[{"name":"node","type":"address"},{"name":"roundId","type":"uint256"},
	           {"name":"amount","type":"uint256"},{"name":"reason","type":"bytes32"}],
	 "outputs":[{"type":"uint256"}]},
	{"name":"AlreadySlashedForRound","type":"error",
	 "inputs":[{"name":"node","type":"address"},{"name":"roundId","type":"uint256"}]}
]`

var (
	slashABI               abi.ABI
	alreadySlashedSelector []byte
)

func init() {
	parsed, err := abi.JSON(strings.NewReader(slasherABI))
	if err != nil {
		panic("evm: slasher ABI is invalid: " + err.Error())
	}
	slashABI = parsed

	sig := crypto.Keccak256([]byte("AlreadySlashedForRound(address,uint256)"))
	alreadySlashedSelector = sig[:4]
}

// Slasher submits penalties with the SLASHER_ROLE key.
//
// This is the only component in the backend that signs a state-changing transaction. It does one
// thing, and the key it holds can do exactly that one thing on chain — the role is granted for
// slash() alone.
type Slasher struct {
	client   *Client
	staking  common.Address
	key      NodeKey
	chainID  *big.Int
	gasLimit uint64
}

func NewSlasher(client *Client, staking pbtypes.Identity, key NodeKey, gasLimit uint64) (*Slasher, error) {
	addr, err := staking.EVMAddress()
	if err != nil {
		return nil, fmt.Errorf("staking address: %w", err)
	}
	if gasLimit == 0 {
		gasLimit = 300_000
	}
	return &Slasher{
		client:   client,
		staking:  common.Address(addr),
		key:      key,
		chainID:  big.NewInt(client.ChainID()),
		gasLimit: gasLimit,
	}, nil
}

// SignerAddress is logged at startup so an operator can confirm which key is in use without the
// key itself appearing anywhere.
func (s *Slasher) SignerAddress() string { return s.key.Address() }

// Slash submits a penalty and returns the transaction hash.
//
// The call is simulated first. A doomed transaction costs gas and lands a failed receipt in the
// audit trail for no reason, and simulating is also how the per-round guard is detected as a
// distinct outcome rather than an opaque revert.
func (s *Slasher) Slash(ctx context.Context, node string, roundID, amount pbtypes.Raw, reason string) (string, error) {
	nodeID, err := pbtypes.IdentityFromEVMHex(node)
	if err != nil {
		return "", fmt.Errorf("node address %q: %w", node, err)
	}
	nodeAddr, err := nodeID.EVMAddress()
	if err != nil {
		return "", err
	}

	input, err := slashABI.Pack("slash", common.Address(nodeAddr), roundID.Big(), amount.Big(), reasonBytes32(reason))
	if err != nil {
		return "", fmt.Errorf("pack slash: %w", err)
	}

	from := common.HexToAddress(s.key.Address())
	call := ethereum.CallMsg{From: from, To: &s.staking, Data: input}

	if _, err := s.client.rpc.CallContract(ctx, call, nil); err != nil {
		if isAlreadySlashed(err) {
			// The sentinel belongs to the package that defines the Slasher interface. Declaring a
			// second one here with the same text would compare unequal under errors.Is — which is
			// exactly the bug the end-to-end retry test caught.
			return "", oracle.ErrAlreadySlashed
		}
		return "", fmt.Errorf("slash would revert: %w", err)
	}

	nonce, err := s.client.rpc.PendingNonceAt(ctx, from)
	if err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	gasPrice, err := s.client.rpc.SuggestGasPrice(ctx)
	if err != nil {
		return "", fmt.Errorf("gas price: %w", err)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &s.staking,
		Value:    big.NewInt(0),
		Gas:      s.gasLimit,
		GasPrice: gasPrice,
		Data:     input,
	})

	signed, err := types.SignTx(tx, types.NewEIP155Signer(s.chainID), s.key.private)
	if err != nil {
		return "", fmt.Errorf("sign slash: %w", err)
	}
	if err := s.client.rpc.SendTransaction(ctx, signed); err != nil {
		return "", fmt.Errorf("send slash: %w", err)
	}

	return signed.Hash().Hex(), nil
}

// isAlreadySlashed matches the custom error's selector in the revert data. Matching the selector
// rather than a message keeps this working when node software changes how it renders reverts.
func isAlreadySlashed(err error) bool {
	var dataErr interface{ ErrorData() any }
	if !errors.As(err, &dataErr) {
		return strings.Contains(err.Error(), "AlreadySlashedForRound")
	}

	hexData, ok := dataErr.ErrorData().(string)
	if !ok {
		return false
	}
	raw := common.FromHex(hexData)
	return len(raw) >= 4 && bytes.Equal(raw[:4], alreadySlashedSelector)
}

// reasonBytes32 right-pads a reason code, matching how the contract and the indexer treat it.
func reasonBytes32(reason string) [32]byte {
	var out [32]byte
	copy(out[:], reason)
	return out
}
