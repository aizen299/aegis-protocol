// Package vaultengine holds the EVM ABI for contracts/src/vault/VaultEngine.sol.
//
// Regenerate with `make backend-abi` after any change to the contract's external surface.
// This package is EVM-specific by definition and is imported only from internal/chain/evm.
package vaultengine

import (
	_ "embed"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

//go:embed VaultEngine.abi.json
var abiJSON string

// Event names consumed by the indexer.
const (
	EventDeposited = "Deposited"
	EventWithdrawn = "Withdrawn"
)

var parsed abi.ABI

func init() {
	a, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		panic("vaultengine: embedded ABI is invalid: " + err.Error())
	}
	parsed = a
}

// ABI returns the parsed contract ABI.
func ABI() abi.ABI { return parsed }
