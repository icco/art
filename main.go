// Command art-server is the API and job-queue entry point for the art service.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.icco.me/art/lib/api"
	"go.icco.me/art/lib/api/handlers"
	"go.icco.me/art/lib/config"
	"go.icco.me/art/lib/db"
	"go.icco.me/art/lib/email"
	"go.icco.me/art/lib/oauth"
	"go.icco.me/art/lib/queue"
	"go.icco.me/art/lib/settings"
	gutillog "go.icco.me/gutil/logging"
	"go.icco.me/gutil/tracing"
	"go.uber.org/zap"
)

func main() {
	log, err := gutillog.NewLogger("art")
	if err != nil {
		panic(err) // logger setup can't itself log; nothing else to do
	}
	defer gutillog.Sync(log)

	ctx := context.Background()
	shutdown, err := tracing.Init(ctx, "art")
	if err != nil {
		log.Fatalw("tracing init", "err", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(shutdownCtx); err != nil {
			log.Warnw("tracing shutdown", "err", err)
		}
	}()

	if err := run(log); err != nil {
		log.Fatalw("fatal", "err", err)
	}
}

func run(log *zap.SugaredLogger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = gutillog.NewContext(ctx, log)

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

	// Env config seeds the settings store; DB rows win once written.
	settingsStore := settings.New(gdb, cfg)

	triager := &email.Runner{Cfg: cfg, DB: gdb, OAuth: oauthFlow, Settings: settingsStore}

	worker := queue.New(gdb, triager)

	h := &handlers.Handlers{
		Cfg:      cfg,
		DB:       gdb,
		OAuth:    oauthFlow,
		Jobs:     worker,
		Triage:   triager,
		Settings: settingsStore,
	}
	router := api.NewRouter(api.Deps{Cfg: cfg, DB: gdb, H: h, Log: log})

	if err := worker.Start(ctx); err != nil {
		return err
	}
	defer worker.Stop()

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
			stop() // cancel the worker so worker.Stop() isn't stuck behind a job
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
