package types

import "testing"

// Two chains sharing an ID would merge their rows: every uniqueness constraint is composite on it.
func TestNoTwoChainsShareAnIDOrName(t *testing.T) {
	ids, names := map[int64]string{}, map[string]bool{}
	for _, c := range KnownChains() {
		if prev, ok := ids[c.ID]; ok {
			t.Errorf("%s and %s share chain ID %d", prev, c.Name, c.ID)
		}
		if names[c.Name] {
			t.Errorf("chain name %s is used twice", c.Name)
		}
		ids[c.ID], names[c.Name] = c.Name, true
	}
}

// An EVM chain's ID is its own; every other chain's is assigned from the reserved range, which is
// what keeps an assignment from colliding with an EVM chain added later.
func TestEachChainIDIsInItsVMsRange(t *testing.T) {
	for _, c := range KnownChains() {
		switch c.VM {
		case VMEVM:
			if !IsEVMChain(c.ID) {
				t.Errorf("%s is EVM but %d is in the reserved range", c.Name, c.ID)
			}
		case VMSVM:
			if c.ID <= NonEVMChainIDBase {
				t.Errorf("%s is not EVM but %d is outside the reserved range", c.Name, c.ID)
			}
		default:
			t.Errorf("%s has unknown VM %q", c.Name, c.VM)
		}
	}
}

func TestAnUnknownChainIsNotFound(t *testing.T) {
	for _, id := range []int64{0, -1, 1, NonEVMChainIDBase, NonEVMChainIDBase + 99} {
		if c, ok := LookupChain(id); ok {
			t.Errorf("LookupChain(%d) = %+v, want not found", id, c)
		}
	}
	if c, ok := LookupChain(ChainIDSolanaLocalnet); !ok || c.VM != VMSVM {
		t.Errorf("LookupChain(solana-localnet) = %+v, %v", c, ok)
	}
}
