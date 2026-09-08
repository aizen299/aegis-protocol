package api

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// A feed identifier is a bytes32 rendered as lowercase hex. Unlike an account address it has no
// chain-specific encoding, so a shape check is the right validation here.
var feedIDPattern = regexp.MustCompile(`^0x[0-9a-f]{64}$`)

func (h *handlers) listOracleFeeds(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	feeds, err := h.oracle.Feeds(r.Context(), limit, offset)
	if err != nil {
		h.log.Error().Err(err).Msg("list feeds failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load feeds")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, feeds))
}

func (h *handlers) getOracleFeed(w http.ResponseWriter, r *http.Request) {
	feedID, ok := canonicalFeedID(chi.URLParam(r, "feedId"))
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_FEED_ID", "feed id must be 0x-prefixed 32-byte hex")
		return
	}

	feed, err := h.oracle.Feed(r.Context(), feedID)
	if err != nil {
		h.writeLookupError(w, err, "feed")
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

func (h *handlers) listOracleRounds(w http.ResponseWriter, r *http.Request) {
	feedID, ok := canonicalFeedID(chi.URLParam(r, "feedId"))
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_FEED_ID", "feed id must be 0x-prefixed 32-byte hex")
		return
	}
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	rounds, err := h.oracle.Rounds(r.Context(), feedID, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Str("feed", feedID).Msg("list rounds failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load rounds")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, rounds))
}

func (h *handlers) getOracleRound(w http.ResponseWriter, r *http.Request) {
	roundID, err := types.ParseRaw(chi.URLParam(r, "roundId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ROUND_ID", "round id must be a decimal integer")
		return
	}

	round, err := h.oracle.Round(r.Context(), roundID)
	if err != nil {
		h.writeLookupError(w, err, "round")
		return
	}
	writeJSON(w, http.StatusOK, round)
}

func (h *handlers) listOracleSubmissions(w http.ResponseWriter, r *http.Request) {
	roundID, err := types.ParseRaw(chi.URLParam(r, "roundId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ROUND_ID", "round id must be a decimal integer")
		return
	}
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	submissions, err := h.oracle.Submissions(r.Context(), roundID, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Str("round", roundID.String()).Msg("list submissions failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load submissions")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, submissions))
}

func (h *handlers) listOracleNodes(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := h.pagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error())
		return
	}

	nodes, err := h.oracle.Nodes(r.Context(), limit, offset)
	if err != nil {
		h.log.Error().Err(err).Msg("list nodes failed")
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load nodes")
		return
	}
	writeJSON(w, http.StatusOK, page(h.chainID, limit, offset, nodes))
}

func (h *handlers) getOracleNode(w http.ResponseWriter, r *http.Request) {
	address, ok := h.canonicalAddress(chi.URLParam(r, "address"))
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ADDRESS", "address is not valid for this chain")
		return
	}

	node, err := h.oracle.Node(r.Context(), address)
	if err != nil {
		h.writeLookupError(w, err, "node")
		return
	}
	writeJSON(w, http.StatusOK, node)
}

// writeLookupError maps an absent row to 404 and everything else to 500. Without the distinction a
// caller cannot tell "this does not exist" from "the database is down", and would retry the wrong
// one of the two.
func (h *handlers) writeLookupError(w http.ResponseWriter, err error, subject string) {
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", subject+" not found")
		return
	}
	h.log.Error().Err(err).Str("subject", subject).Msg("lookup failed")
	writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load "+subject)
}

func canonicalFeedID(raw string) (string, bool) {
	lowered := toLowerASCII(raw)
	if !feedIDPattern.MatchString(lowered) {
		return "", false
	}
	return lowered, true
}

// toLowerASCII avoids strings.ToLower's Unicode handling on a value that must be hex or rejected.
func toLowerASCII(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}

func page[T any](chainID int64, limit, offset int, items []T) map[string]any {
	return map[string]any{
		"chainId": chainID,
		"limit":   limit,
		"offset":  offset,
		"count":   len(items),
		"items":   items,
	}
}
