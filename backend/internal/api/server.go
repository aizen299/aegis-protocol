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
	"github.com/aizen299/aegis-protocol/backend/internal/vault"
	"github.com/aizen299/aegis-protocol/backend/pkg/config"
)

type Server struct {
	http *http.Server
	log  zerolog.Logger
	cfg  *config.Config
}

type Deps struct {
	Store   *db.Store
	Cache   *cache.Client
	Vault   *vault.Service
	Oracle  OracleService
	Chain   chain.Client
	ChainID int64
}

func NewServer(cfg *config.Config, log zerolog.Logger, deps Deps) *Server {
	h := &handlers{
		vault:       deps.Vault,
		oracle:      deps.Oracle,
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
