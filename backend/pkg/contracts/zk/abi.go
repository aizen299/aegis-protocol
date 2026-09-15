// Package zk holds the EVM ABIs for contracts/src/zk.
//
// Regenerate with `make backend-abi` after any change to either contract's external surface.
// EVM-specific by definition; imported only from internal/chain/evm and the indexer wiring.
package zk

import (
	_ "embed"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

//go:embed CommitmentTree.abi.json
var treeJSON string

//go:embed ZkVaultGate.abi.json
var gateJSON string

// Event names the indexer consumes. Anything not listed here is emitted but not indexed.
const (
	EventCommitmentInserted    = "CommitmentInserted"
	EventPrivateActionExecuted = "PrivateActionExecuted"
	EventActionRegistered      = "ActionRegistered"
	EventActionDeregistered    = "ActionDeregistered"
)

var (
	treeABI abi.ABI
	gateABI abi.ABI
)

func init() {
	tree, err := abi.JSON(strings.NewReader(treeJSON))
	if err != nil {
		panic("zk: embedded CommitmentTree ABI is invalid: " + err.Error())
	}
	treeABI = tree

	gate, err := abi.JSON(strings.NewReader(gateJSON))
	if err != nil {
		panic("zk: embedded ZkVaultGate ABI is invalid: " + err.Error())
	}
	gateABI = gate
}

// TreeABI returns the parsed CommitmentTree ABI.
func TreeABI() abi.ABI { return treeABI }

// GateABI returns the parsed ZkVaultGate ABI.
func GateABI() abi.ABI { return gateABI }
