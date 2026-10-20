// Command api serves the lists HTTP API. main is the composition root:
// the only place that knows every concrete type.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vsheshjain/code-taste-golang/example/internal/delivery/rest"
	"github.com/vsheshjain/code-taste-golang/example/internal/domain/listdom"
	"github.com/vsheshjain/code-taste-golang/example/internal/repo/listrepo"
	"github.com/vsheshjain/code-taste-golang/example/internal/service/listsvc"
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

	addr := cmp.Or(os.Getenv("ADDR"), ":8080")
	dataFile := cmp.Or(os.Getenv("DATA_FILE"), "lists.json")

	svc := listsvc.New(listrepo.New(dataFile), func() listdom.ID { return listdom.ID(rand.Text()) })
	srv := &http.Server{
		Addr:              addr,
		Handler:           rest.NewHandler(svc, log),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", addr, "data_file", dataFile)

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
