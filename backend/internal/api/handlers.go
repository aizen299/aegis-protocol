package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/cache"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/vault"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// OracleService is the oracle read surface the handlers depend on. An interface rather than the
// concrete service so handler tests can exercise validation, error mapping, and serialisation
// without a database behind them.
// IdentityCodec is all the handlers need from a chain: the canonical encoding of an account
// identity. Narrowed from chain.Client because requiring log-reading and head-tracking to validate
// an address string is coupling the API does not use and a test would have to fake for nothing.
type IdentityCodec interface {
	EncodeIdentity(id types.Identity) string
	DecodeIdentity(s string) (types.Identity, error)
}

// OracleService is the oracle read surface the handlers depend on. An interface rather than the
// concrete service so handler tests can exercise validation, error mapping, and serialisation
// without a database behind them.
type OracleService interface {
	Feeds(ctx context.Context, limit, offset int) ([]types.OracleFeed, error)
	Feed(ctx context.Context, feedID string) (types.OracleFeed, error)
	Rounds(ctx context.Context, feedID string, limit, offset int) ([]types.OracleRound, error)
	Round(ctx context.Context, roundID types.Raw) (types.OracleRound, error)
	Submissions(ctx context.Context, roundID types.Raw, limit, offset int) ([]types.OracleSubmission, error)
	Nodes(ctx context.Context, limit, offset int) ([]types.OracleNode, error)
	Node(ctx context.Context, address string) (types.OracleNode, error)
}

type handlers struct {
	vault       *vault.Service
	oracle      OracleService
	governance  GovernanceService
	store       *db.Store
	cache       *cache.Client
	chainClient IdentityCodec
	chainID     int64
	maxPageSize int
	log         zerolog.Logger
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: msg, Code: code})
}

func (h *handlers) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handlers) ready(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := h.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database unreachable")
		return
	}
	if err := h.cache.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "CACHE_UNAVAILABLE", "cache unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// canonicalAddress validates the path parameter against the chain's own encoding and returns the
// canonical form. Identity encodings are chain-specific, so this cannot be a hex regex.
func (h *handlers) canonicalAddress(raw string) (string, bool) {
	id, err := h.chainClient.DecodeIdentity(raw)
	if err != nil {
		return "", false
	}
	return h.chainClient.EncodeIdentity(id), true
}

func (h *handlers) getVaultPosition(w http.ResponseWriter, r *http.Request) {
	address, ok := h.canonicalAddress(chi.URLParam(r, "address"))
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ADDRESS", "address is not valid for this chain")
		return
	}

	pos, err := h.vault.Position(r.Context(), address)
	if err != nil {
		h.log.Error().Err(err).Str("address", address).Msg("vault position lookup failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load position")
		return
	}
	writeJSON(w, http.StatusOK, pos)
}

func (h *handlers) getVaultTVL(w http.ResponseWriter, r *http.Request) {
	vaultAddress, ok := h.canonicalAddress(chi.URLParam(r, "vaultAddress"))
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ADDRESS", "vault address is not valid for this chain")
		return
	}

	tvl, err := h.vault.TVL(r.Context(), vaultAddress)
	if err != nil {
		h.log.Error().Err(err).Str("vault", vaultAddress).Msg("vault tvl lookup failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load tvl")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"chainId": h.chainID,
		"tvl":     tvl,
	})
}

func (h *handlers) listVaultDeposits(w http.ResponseWriter, r *http.Request) {
	address, ok := h.canonicalAddress(chi.URLParam(r, "address"))
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ADDRESS", "address is not valid for this chain")
		return
	}

	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	deposits, err := h.vault.Deposits(r.Context(), address, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Str("address", address).Msg("vault deposits lookup failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load deposits")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"chainId": h.chainID,
		"limit":   limit,
		"offset":  offset,
		"items":   deposits,
	})
}

func (h *handlers) pagination(r *http.Request) (limit, offset int, err error) {
	limit = h.maxPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			return 0, 0, errInvalidLimit
		}
		if limit > h.maxPageSize {
			limit = h.maxPageSize
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, errInvalidOffset
		}
	}
	return limit, offset, nil
}
