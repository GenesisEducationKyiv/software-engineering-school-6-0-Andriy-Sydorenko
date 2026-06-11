package platform

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}

// RunAdminServer starts the admin HTTP server on addr in the background and
// returns it so the caller can Shutdown it gracefully.
func RunAdminServer(addr string) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           AdminHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("admin server stopped", "err", err)
		}
	}()
	return srv
}
