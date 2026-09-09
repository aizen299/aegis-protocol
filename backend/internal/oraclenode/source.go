package oraclenode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Source is one independent price feed.
type Source interface {
	Name() string
	Fetch(ctx context.Context, decimals uint8) (types.Raw, error)
}

// HTTPSource reads a price from a JSON endpoint.
type HTTPSource struct {
	SourceName string
	URL        string
	// Path selects the value, dot-separated ("data.amount"). Kept deliberately simple: a source
	// needing more than this is better wrapped in its own Source implementation than expressed in
	// a query language nobody can test.
	Path    string
	Client  *http.Client
	Timeout time.Duration
}

func (s *HTTPSource) Name() string { return s.SourceName }

func (s *HTTPSource) Fetch(ctx context.Context, decimals uint8) (types.Raw, error) {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return types.Raw{}, fmt.Errorf("build request: %w", err)
	}

	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return types.Raw{}, fmt.Errorf("request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return types.Raw{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Bounded read: a source that streams forever must not hold a round open.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return types.Raw{}, fmt.Errorf("read body: %w", err)
	}

	raw, err := extractPath(body, s.Path)
	if err != nil {
		return types.Raw{}, err
	}
	return ParseScaled(raw, decimals)
}

// extractPath pulls a scalar out of a JSON document by dot path, rendering it as the literal string
// the document contained. Numbers are taken verbatim rather than through float64, so a price does
// not lose digits before it is even parsed.
func extractPath(body []byte, path string) (string, error) {
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		return "", fmt.Errorf("decode json: %w", err)
	}

	current := document
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return "", fmt.Errorf("path %q: %q is not an object", path, segment)
		}
		current, ok = object[segment]
		if !ok {
			return "", fmt.Errorf("path %q: %q is missing", path, segment)
		}
	}

	switch value := current.(type) {
	case string:
		return value, nil
	case json.Number:
		return value.String(), nil
	case float64:
		// encoding/json produced a float because the document was decoded into any. Re-extract the
		// literal text so precision is not lost between the wire and the parser.
		return strconv.FormatFloat(value, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("path %q holds %T, want a number or string", path, current)
	}
}

// ErrTooFewSources is returned when not enough sources answered to form a trustworthy value.
var ErrTooFewSources = errors.New("too few sources responded")

// FetchResult records what each source said, including the ones that failed. The failures are kept
// because a source that is consistently down is an operational problem worth seeing.
type FetchResult struct {
	Values  []types.Raw
	Sources []string
	Failed  map[string]error
}
