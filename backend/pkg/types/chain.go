package types

// EVM chain IDs are the canonical numeric IDs. Non-EVM chains have no numeric ID of their own, so
// the protocol assigns them internal IDs from a reserved high range. Nothing in that range may
// collide with a real EVM chain ID.
//
// Phase 1 is single-chain (Arbitrum). Concrete non-EVM assignments are made in Phase 2 — see
// docs/project-spec.md §7.
const (
	ChainIDArbitrumOne     int64 = 42161
	ChainIDArbitrumSepolia int64 = 421614
	ChainIDAnvil           int64 = 31337

	// NonEVMChainIDBase is the floor of the reserved range for internally-assigned chain IDs.
	NonEVMChainIDBase int64 = 1 << 62
)

// IsEVMChain reports whether id refers to a real EVM chain rather than an internal assignment.
func IsEVMChain(id int64) bool {
	return id > 0 && id < NonEVMChainIDBase
}
