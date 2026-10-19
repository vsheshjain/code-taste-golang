// Command api serves the lists HTTP API. main is the composition root:
// the only place that knows every concrete type.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vsheshjain/code-taste-golang/example/internal/app"
	"github.com/vsheshjain/code-taste-golang/example/internal/httpapi"
	"github.com/vsheshjain/code-taste-golang/example/internal/list"
	"github.com/vsheshjain/code-taste-golang/example/internal/memory"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	svc := app.New(&memory.Store{}, func() list.ID { return list.ID(rand.Text()) })
	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.NewHandler(svc, log),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
