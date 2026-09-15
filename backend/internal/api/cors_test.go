package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsHandler(origins []string) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return cors(origins)(inner)
}

func TestAListedOriginIsAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/oracle/feeds", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()

	corsHandler([]string{"http://localhost:3000"}).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow-origin = %q, want the listed origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q; a cached response must not be served to a different origin", got)
	}
}

// The header is what lets a browser read the response, so an unlisted origin getting one would make
// the allowlist decorative.
func TestAnUnlistedOriginGetsNoHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/oracle/feeds", nil)
	req.Header.Set("Origin", "https://not-ours.example")
	rec := httptest.NewRecorder()

	corsHandler([]string{"http://localhost:3000"}).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin = %q for an unlisted origin", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; an unlisted origin is refused by the browser, not by the server", rec.Code)
	}
}

// Empty configuration must mean nobody, never everybody.
func TestNoConfiguredOriginsAllowsNobody(t *testing.T) {
	for _, origins := range [][]string{nil, {}, {""}, {"  "}} {
		req := httptest.NewRequest(http.MethodGet, "/v1/oracle/feeds", nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()

		corsHandler(origins).ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origins %q produced allow-origin %q; empty must mean nobody", origins, got)
		}
	}
}

func TestAPreflightIsAnsweredWithoutReachingTheHandler(t *testing.T) {
	reached := false
	handler := cors([]string{"http://localhost:3000"})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/oracle/feeds", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if reached {
		t.Error("a preflight reached the route handler")
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("preflight carried no allowed methods")
	}
}

// A wildcard is never returned, even when one is configured by mistake.
func TestAWildcardIsNotEchoedAsAnOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/oracle/feeds", nil)
	req.Header.Set("Origin", "https://anything.example")
	rec := httptest.NewRecorder()

	corsHandler([]string{"*"}).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin = %q; \"*\" is not treated as a matching origin", got)
	}
}
