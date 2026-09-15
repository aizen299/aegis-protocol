package api

import (
	"context"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// A commitment, nullifier, root, or action id is a bytes32 rendered as lowercase hex. Unlike an
// account address it has no chain-specific encoding, so a shape check is the right validation.
var bytes32Pattern = regexp.MustCompile(`^0x[0-9a-f]{64}$`)

// ZkService is the read surface the zk endpoints need.
type ZkService interface {
	Gate(ctx context.Context) (types.ZkGateMetadata, error)
	AnonymitySet(ctx context.Context, tree string) (types.AnonymitySet, error)
	Commitments(ctx context.Context, tree string, limit, offset int) ([]types.Commitment, error)
	PrivateActions(ctx context.Context, gate string, limit, offset int) ([]types.PrivateAction, error)
	NullifierSpent(ctx context.Context, gate, nullifier string) (bool, error)
	Actions(ctx context.Context, gate string, limit, offset int) ([]types.ZkAction, error)
}

func (h *handlers) getZkGate(w http.ResponseWriter, r *http.Request) {
	gate, err := h.zk.Gate(r.Context())
	if err != nil {
		h.writeLookupError(w, err, "zk gate")
		return
	}
	writeJSON(w, http.StatusOK, gate)
}

// The anonymity set is served as its own endpoint because it is a caveat, not a statistic: a
// membership proof is only as private as the number of members, and at one member it is not
// private at all. See docs/v0.4-zk-plan.md §4.
func (h *handlers) getAnonymitySet(w http.ResponseWriter, r *http.Request) {
	gate, err := h.zk.Gate(r.Context())
	if err != nil {
		h.writeLookupError(w, err, "zk gate")
		return
	}

	set, err := h.zk.AnonymitySet(r.Context(), gate.TreeAddress)
	if err != nil {
		h.log.Error().Err(err).Msg("anonymity set lookup failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load the anonymity set")
		return
	}
	writeJSON(w, http.StatusOK, set)
}

// Commitments come back in leaf order, which is the order a Merkle path is built from. Any other
// ordering describes a different tree.
func (h *handlers) listCommitments(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	gate, err := h.zk.Gate(r.Context())
	if err != nil {
		h.writeLookupError(w, err, "zk gate")
		return
	}

	commitments, err := h.zk.Commitments(r.Context(), gate.TreeAddress, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Msg("list commitments failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load commitments")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, commitments))
}

func (h *handlers) listPrivateActions(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	gate, err := h.zk.Gate(r.Context())
	if err != nil {
		h.writeLookupError(w, err, "zk gate")
		return
	}

	actions, err := h.zk.PrivateActions(r.Context(), gate.Address, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Msg("list private actions failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load private actions")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, actions))
}

// Answers the one question worth asking before paying to generate a proof.
func (h *handlers) getNullifierStatus(w http.ResponseWriter, r *http.Request) {
	nullifier := chi.URLParam(r, "nullifier")
	if !bytes32Pattern.MatchString(nullifier) {
		writeError(w, http.StatusBadRequest, "INVALID_NULLIFIER",
			"nullifier must be 0x-prefixed lowercase 32-byte hex")
		return
	}

	gate, err := h.zk.Gate(r.Context())
	if err != nil {
		h.writeLookupError(w, err, "zk gate")
		return
	}

	spent, err := h.zk.NullifierSpent(r.Context(), gate.Address, nullifier)
	if err != nil {
		h.log.Error().Err(err).Msg("nullifier lookup failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load the nullifier")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"chainId":   h.chainID,
		"gate":      gate.Address,
		"nullifier": nullifier,
		"spent":     spent,
	})
}

func (h *handlers) listZkActions(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	gate, err := h.zk.Gate(r.Context())
	if err != nil {
		h.writeLookupError(w, err, "zk gate")
		return
	}

	actions, err := h.zk.Actions(r.Context(), gate.Address, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Msg("list zk actions failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load actions")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, actions))
}
