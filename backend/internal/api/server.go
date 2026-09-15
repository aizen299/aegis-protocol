// Package api serves the REST surface. net/http + chi, no heavy framework.
package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/internal/cache"
	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/internal/observability"
	"github.com/aizen299/aegis-protocol/backend/internal/vault"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
)

type Server struct {
	http *http.Server
	log  zerolog.Logger
	cfg  *config.Config
}

type Deps struct {
	Store      *db.Store
	Cache      *cache.Client
	Vault      *vault.Service
	Oracle     OracleService
	Governance GovernanceService
	Zk         ZkService
	Chain      chain.Client
	ChainID    int64

	// Metrics is optional. Nil disables emission, which is what handler tests use.
	Metrics *observability.Metrics
}

func NewServer(cfg *config.Config, log zerolog.Logger, deps Deps) *Server {
	h := &handlers{
		vault:       deps.Vault,
		oracle:      deps.Oracle,
		governance:  deps.Governance,
		zk:          deps.Zk,
		metrics:     deps.Metrics,
		store:       deps.Store,
		cache:       deps.Cache,
		chainClient: deps.Chain,
		chainID:     deps.ChainID,
		maxPageSize: cfg.API.MaxPageSize,
		log:         log,
	}

	return &Server{
		log: log,
		cfg: cfg,
		http: &http.Server{
			Addr:         cfg.API.Addr,
			Handler:      routes(cfg, h, log),
			ReadTimeout:  cfg.API.ReadTimeout,
			WriteTimeout: cfg.API.WriteTimeout,
		},
	}
}

// routes builds the router. Separate from NewServer so handler tests can drive the real routing
// and middleware without binding a port or constructing a database.
func routes(cfg *config.Config, h *handlers, log zerolog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(cfg.API.WriteTimeout))
	r.Use(requestLogger(log))
	r.Use(requestMetrics(h.metrics))

	r.Get("/health", h.health)
	r.Get("/ready", h.ready)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/vault/{vaultAddress}/tvl", h.getVaultTVL)
		r.Get("/vault/positions/{address}", h.getVaultPosition)
		r.Get("/vault/positions/{address}/deposits", h.listVaultDeposits)

		r.Get("/oracle/feeds", h.listOracleFeeds)
		r.Get("/oracle/feeds/{feedId}", h.getOracleFeed)
		r.Get("/oracle/feeds/{feedId}/rounds", h.listOracleRounds)
		r.Get("/oracle/rounds/{roundId}", h.getOracleRound)
		r.Get("/oracle/rounds/{roundId}/submissions", h.listOracleSubmissions)
		r.Get("/oracle/nodes", h.listOracleNodes)
		r.Get("/oracle/nodes/{address}", h.getOracleNode)

		r.Get("/governance/governor", h.getGovernor)
		r.Get("/governance/proposals", h.listProposals)
		r.Get("/governance/proposals/{proposalId}", h.getProposal)
		r.Get("/governance/proposals/{proposalId}/votes", h.listProposalVotes)
		r.Get("/governance/voters/{address}/votes", h.listVoterVotes)

		r.Get("/zk/gate", h.getZkGate)
		r.Get("/zk/anonymity-set", h.getAnonymitySet)
		r.Get("/zk/commitments", h.listCommitments)
		r.Get("/zk/actions", h.listZkActions)
		r.Get("/zk/private-actions", h.listPrivateActions)
		r.Get("/zk/nullifiers/{nullifier}", h.getNullifierStatus)
	})

	return r
}

// Handler returns the routed handler, so the API can be exercised in-process without binding a
// port.
func (s *Server) Handler() http.Handler { return s.http.Handler }

func (s *Server) Start() error {
	s.log.Info().Str("addr", s.cfg.API.Addr).Msg("api listening")
	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// requestMetrics publishes the two measurements the required alarms need: latency, from which
// CloudWatch derives p99, and the request and 5xx counts an error rate is computed from.
//
// The rate is deliberately not computed here. A service that reports its own error rate reports it
// from inside the thing that may be failing, and a percentage emitted per request is meaningless
// anyway — the alarm divides two sums over its own window.
//
// Health and readiness are excluded: they are polled by the load balancer far more often than any
// real endpoint, and including them would dilute both the latency percentile and the error rate
// until neither described user traffic.
func requestMetrics(metrics *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if metrics == nil || r.URL.Path == "/health" || r.URL.Path == "/ready" {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			serverErrors := 0.0
			if ww.Status() >= http.StatusInternalServerError {
				serverErrors = 1
			}

			metrics.Emit(
				observability.Measurement{
					Name:  observability.MetricAPILatencyMillis,
					Value: float64(time.Since(start).Milliseconds()),
					Unit:  observability.UnitMilliseconds,
				},
				observability.Measurement{
					Name:  observability.MetricAPIRequests,
					Value: 1,
					Unit:  observability.UnitCount,
				},
				observability.Measurement{
					Name:  observability.MetricAPIServerErrors,
					Value: serverErrors,
					Unit:  observability.UnitCount,
				},
			)
		})
	}
}

func requestLogger(log zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			log.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", ww.Status()).
				Dur("duration", time.Since(start)).
				Str("request_id", middleware.GetReqID(r.Context())).
				Msg("request")
		})
	}
}
