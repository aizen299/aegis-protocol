package api

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// GovernanceService is the read surface the governance endpoints need.
type GovernanceService interface {
	Proposals(ctx context.Context, state string, limit, offset int) ([]types.Proposal, error)
	Proposal(ctx context.Context, proposalID types.Raw) (types.Proposal, error)
	ProposalVotes(ctx context.Context, proposalID types.Raw, limit, offset int) ([]types.Vote, error)
	VotesByVoter(ctx context.Context, voter string, limit, offset int) ([]types.Vote, error)
	Governor(ctx context.Context) (types.GovernorMetadata, error)
}

// validProposalStates are the filter values a caller may pass. An unknown one is rejected rather
// than silently returning everything: a typo that quietly widens a filter is worse than an error.
var validProposalStates = map[string]bool{
	types.ProposalStatePending:    true,
	types.ProposalStateActive:     true,
	types.ProposalStateSucceeded:  true,
	types.ProposalStateDefeated:   true,
	types.ProposalStateQueued:     true,
	types.ProposalStateDispatched: true,
	types.ProposalStateExecuted:   true,
	types.ProposalStateFailed:     true,
	types.ProposalStateCancelled:  true,
}

func (h *handlers) listProposals(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	state := r.URL.Query().Get("state")
	if state != "" && !validProposalStates[state] {
		writeError(w, http.StatusBadRequest, "INVALID_STATE", "unknown proposal state")
		return
	}

	proposals, err := h.governance.Proposals(r.Context(), state, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Str("state", state).Msg("list proposals failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load proposals")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, proposals))
}

func (h *handlers) getProposal(w http.ResponseWriter, r *http.Request) {
	proposalID, err := types.ParseRaw(chi.URLParam(r, "proposalId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PROPOSAL_ID", "proposal id must be a decimal integer")
		return
	}

	proposal, err := h.governance.Proposal(r.Context(), proposalID)
	if err != nil {
		h.writeLookupError(w, err, "proposal")
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (h *handlers) listProposalVotes(w http.ResponseWriter, r *http.Request) {
	proposalID, err := types.ParseRaw(chi.URLParam(r, "proposalId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PROPOSAL_ID", "proposal id must be a decimal integer")
		return
	}
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	votes, err := h.governance.ProposalVotes(r.Context(), proposalID, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Str("proposal", proposalID.String()).Msg("list proposal votes failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load votes")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, votes))
}

func (h *handlers) listVoterVotes(w http.ResponseWriter, r *http.Request) {
	voter, ok := h.canonicalAddress(chi.URLParam(r, "address"))
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ADDRESS", "address is not valid for this chain")
		return
	}
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	votes, err := h.governance.VotesByVoter(r.Context(), voter, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Str("voter", voter).Msg("list voter votes failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load votes")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, votes))
}

// The governor's own metadata, including the decimals every vote weight on this chain is
// denominated in.
func (h *handlers) getGovernor(w http.ResponseWriter, r *http.Request) {
	governor, err := h.governance.Governor(r.Context())
	if err != nil {
		h.writeLookupError(w, err, "governor")
		return
	}
	writeJSON(w, http.StatusOK, governor)
}
