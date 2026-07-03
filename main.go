// Command art-server is the API/cron entry point for the art service.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/icco/art/lib/agent"
	"github.com/icco/art/lib/api"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/cron"
	"github.com/icco/art/lib/db"
	"github.com/icco/art/lib/email"
	"github.com/icco/art/lib/oauth"
	gutillog "github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

func main() {
	log, err := gutillog.NewLogger("art")
	if err != nil {
		panic(err) // logger setup can't itself log; nothing else to do
	}
	defer gutillog.Sync(log)

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

	syncRunner := &calendar.Runner{DB: gdb, OAuth: oauthFlow, TZ: cfg.Timezone}
	planner := &agent.Planner{Cfg: cfg, DB: gdb, OAuth: oauthFlow}
	triager := &email.Runner{Cfg: cfg, DB: gdb, OAuth: oauthFlow}

	h := &handlers.Handlers{
		Cfg:     cfg,
		DB:      gdb,
		OAuth:   oauthFlow,
		Sync:    syncRunner,
		Planner: planner,
		Triage:  triager,
	}
	router := api.NewRouter(api.Deps{Cfg: cfg, DB: gdb, H: h, Log: log})

	scheduler := cron.New(syncRunner, planner, triager)
	scheduler.Start(ctx)
	defer scheduler.Stop()

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
