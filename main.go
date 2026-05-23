package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/icco/art/lib/api"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/db"
	"github.com/icco/art/lib/logging"
	"github.com/icco/art/lib/oauth"
)

func main() {
	if err := run(); err != nil {
		os.Stderr.WriteString("fatal: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log, err := logging.New(cfg.LogLevel)
	if err != nil {
		return err
	}
	defer log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = logging.Inject(ctx, log)

	gdb, err := db.Open(cfg.DatabaseURL, log.Desugar())
	if err != nil {
		return err
	}

	sealer, err := oauth.NewSealer(cfg.TokenEncKey)
	if err != nil {
		return err
	}
	oauthStore := &oauth.Store{DB: gdb, Sealer: sealer}
	oauthFlow := oauth.NewFlow(cfg.OAuth.ClientID, cfg.OAuth.ClientSecret, cfg.OAuth.RedirectURL, oauthStore)
	syncRunner := &calendar.Runner{DB: gdb, OAuth: oauthFlow}

	h := &handlers.Handlers{
		Cfg:   cfg,
		DB:    gdb,
		OAuth: oauthFlow,
		Sync:  syncRunner,
	}
	router := api.NewRouter(api.Deps{Cfg: cfg, DB: gdb, H: h})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Infow("server listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case <-ctx.Done():
		log.Infow("shutdown signal received")
	case err := <-serveErr:
		if err != nil {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
