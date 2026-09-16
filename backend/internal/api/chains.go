package api

import (
	"context"
	"net/http"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

type chainKey struct{}

func chainMap(chains []ChainDeps) map[int64]*ChainDeps {
	out := make(map[int64]*ChainDeps, len(chains))
	for i := range chains {
		out[chains[i].ID] = &chains[i]
	}
	return out
}

// chainOf is only called behind resolveChain, which guarantees a chain is set.
func chainOf(r *http.Request) *ChainDeps {
	return r.Context().Value(chainKey{}).(*ChainDeps)
}

// resolveChain selects the chain a request is about from its `chain` parameter, a registry name.
// Without one, the API's single chain is used; with several configured, it refuses rather than pick
// one, since an address or id is only meaningful on the chain it came from. §10.8.
func (h *handlers) resolveChain(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names := r.URL.Query()["chain"]
		var selected *ChainDeps

		switch len(names) {
		case 0:
			if len(h.chains) != 1 {
				writeError(w, http.StatusBadRequest, "CHAIN_REQUIRED", "this API serves several chains; name one with ?chain=")
				return
			}
			for _, c := range h.chains {
				selected = c
			}
		case 1:
			info, ok := types.LookupChainByName(names[0])
			if !ok {
				writeError(w, http.StatusBadRequest, "UNKNOWN_CHAIN", "chain is not a known chain name")
				return
			}
			if selected, ok = h.chains[info.ID]; !ok {
				writeError(w, http.StatusNotFound, "CHAIN_NOT_SERVED", "this API does not serve that chain")
				return
			}
		default:
			writeError(w, http.StatusBadRequest, "INVALID_CHAIN", "name exactly one chain")
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chainKey{}, selected)))
	})
}

func requireModule(name string, present func(*ChainDeps) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !present(chainOf(r)) {
				writeError(w, http.StatusNotFound, "MODULE_NOT_ON_CHAIN", name+" is not deployed on this chain")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
