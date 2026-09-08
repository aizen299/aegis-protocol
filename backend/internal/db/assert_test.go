package db

import "github.com/aizen299/aegis-protocol/backend/internal/oracle"

// The aggregation service depends on an interface; this fails the build if the store drifts from
// it, rather than the mismatch surfacing where the two are wired together in a binary.
var _ oracle.AggregatorStore = (*Store)(nil)

// The read service likewise.
var _ oracle.Reader = (*Store)(nil)
