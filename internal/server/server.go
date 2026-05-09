package server

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/johannb/rinha-2026-go/internal/detector"
)

// Server holds the request-time state. The Store is loaded
// asynchronously: a nil pointer means /ready returns 503.
type Server struct {
	store atomic.Pointer[detector.Store]
	mcc   detector.MCCRisk
	norm  detector.Constants
}

func New(mcc detector.MCCRisk, norm detector.Constants) *Server {
	return &Server{mcc: mcc, norm: norm}
}

func (s *Server) SetStore(st *detector.Store) { s.store.Store(st) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", s.handleReady)
	mux.HandleFunc("POST /fraud-score", s.handleFraudScore)
	return mux
}

// ListenAndServe runs the HTTP server until ctx is cancelled, then
// performs a graceful shutdown.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.Handler(),
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}
