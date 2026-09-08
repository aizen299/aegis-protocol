package types

import (
	"math/big"
	"time"
)

// Amounts throughout are raw base units exactly as the chain reported them — a uint256, never
// rescaled at write time. Decimals belong to the asset (see AssetMetadata), not to the protocol:
// scaling on ingest would bake one token's decimals into every row and silently corrupt any asset
// that does not match.
//
// Raw is rendered as a decimal string in JSON so that a uint256 survives a JavaScript client.

// AssetMetadata is what makes a raw amount interpretable.
type AssetMetadata struct {
	ChainID  int64  `json:"chainId"`
	Address  string `json:"address"`
	Decimals uint8  `json:"decimals"`
	Symbol   string `json:"symbol,omitempty"`
	Name     string `json:"name,omitempty"`
}

// VaultDeposit mirrors a Deposited event.
type VaultDeposit struct {
	ChainID      int64     `json:"chainId"`
	UserAddress  string    `json:"user"`
	AssetAddress string    `json:"asset"`
	VaultAddress string    `json:"vault"`
	Amount       Raw       `json:"amount"`
	Shares       Raw       `json:"shares"`
	Decimals     uint8     `json:"decimals"`
	TxHash       string    `json:"txHash"`
	LogIndex     uint      `json:"logIndex"`
	BlockNumber  uint64    `json:"blockNumber"`
	DepositedAt  time.Time `json:"depositedAt"`
}

// VaultWithdrawal mirrors a Withdrawn event.
type VaultWithdrawal struct {
	ChainID      int64     `json:"chainId"`
	UserAddress  string    `json:"user"`
	AssetAddress string    `json:"asset"`
	VaultAddress string    `json:"vault"`
	Amount       Raw       `json:"amount"`
	Shares       Raw       `json:"shares"`
	Decimals     uint8     `json:"decimals"`
	TxHash       string    `json:"txHash"`
	LogIndex     uint      `json:"logIndex"`
	BlockNumber  uint64    `json:"blockNumber"`
	WithdrawnAt  time.Time `json:"withdrawnAt"`
}

// VaultPosition is the aggregated per-user view served by the API and cached in Redis.
//
// ShareDecimals is served again as of the vaults metadata table. It was removed when the backend
// had no way to know the vault's offset, because serving a hardcoded zero was worse than serving
// nothing. The indexer now reads the offset from the contract and records it, so the value is
// resolved rather than assumed.
type VaultPosition struct {
	ChainID        int64      `json:"chainId"`
	UserAddress    string     `json:"user"`
	AssetAddress   string     `json:"asset,omitempty"`
	Shares         Raw        `json:"shares"`
	DepositedTotal Raw        `json:"depositedTotal"`
	WithdrawnTotal Raw        `json:"withdrawnTotal"`
	Decimals       uint8      `json:"decimals"`
	ShareDecimals  uint8      `json:"shareDecimals"`
	LastDepositAt  *time.Time `json:"lastDepositAt,omitempty"`
}

func NewRawFromBig(n *big.Int) Raw {
	if n == nil {
		return Raw{}
	}
	return Raw{value: new(big.Int).Set(n)}
}

// VaultMetadata is what makes a raw share amount interpretable.
//
// Shares are offset from the asset by the vault's virtual-shares defence, so their scale is
// AssetDecimals + ShareOffset. The offset is a property of the deployed contract, which is why it
// is read and recorded rather than assumed — the same reason token decimals are.
type VaultMetadata struct {
	ChainID       int64  `json:"chainId"`
	Address       string `json:"address"`
	AssetAddress  string `json:"asset"`
	ShareOffset   uint8  `json:"shareOffset"`
	AssetDecimals uint8  `json:"assetDecimals"`
}

// ShareDecimals is the scale a share amount is denominated in.
func (v VaultMetadata) ShareDecimals() uint8 {
	return v.AssetDecimals + v.ShareOffset
}
