package types

// EVM chain IDs are the canonical numeric IDs. Non-EVM chains have no numeric ID of their own, so
// the protocol assigns them internal IDs from a reserved high range. Nothing in that range may
// collide with a real EVM chain ID. See docs/project-spec.md §7 and docs/v2.0-solana-plan.md §2.6.
const (
	ChainIDArbitrumOne     int64 = 42161
	ChainIDArbitrumSepolia int64 = 421614
	ChainIDAnvil           int64 = 31337

	// NonEVMChainIDBase is the floor of the reserved range for internally-assigned chain IDs.
	NonEVMChainIDBase int64 = 1 << 62

	// Assignments are permanent: every chain-derived row is keyed by them.
	ChainIDSolanaMainnet  int64 = NonEVMChainIDBase + 1
	ChainIDSolanaDevnet   int64 = NonEVMChainIDBase + 2
	ChainIDSolanaLocalnet int64 = NonEVMChainIDBase + 3
)

// IsEVMChain reports whether id refers to a real EVM chain rather than an internal assignment.
func IsEVMChain(id int64) bool {
	return id > 0 && id < NonEVMChainIDBase
}

type VM string

const (
	VMEVM VM = "evm"
	VMSVM VM = "svm"
)

type Chain struct {
	ID   int64
	Name string
	VM   VM
}

// knownChains is the only place a chain ID is assigned. A service configured with an ID not listed
// here refuses to start.
var knownChains = []Chain{
	{ChainIDArbitrumOne, "arbitrum-one", VMEVM},
	{ChainIDArbitrumSepolia, "arbitrum-sepolia", VMEVM},
	{ChainIDAnvil, "anvil", VMEVM},
	{ChainIDSolanaMainnet, "solana-mainnet", VMSVM},
	{ChainIDSolanaDevnet, "solana-devnet", VMSVM},
	{ChainIDSolanaLocalnet, "solana-localnet", VMSVM},
}

func KnownChains() []Chain {
	return append([]Chain(nil), knownChains...)
}

func LookupChain(id int64) (Chain, bool) {
	for _, c := range knownChains {
		if c.ID == id {
			return c, true
		}
	}
	return Chain{}, false
}
