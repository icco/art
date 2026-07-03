package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	gutillog "github.com/icco/gutil/logging"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// triageLockKey is the app-wide Postgres advisory-lock key for triage passes.
const triageLockKey = 0x4152545f5452 // "ART_TR"

// Runner executes an email-triage pass across all linked accounts and records
// it as an AgentRun row (kind=triage). It mirrors calendar.Runner plus the
// planner's run bookkeeping.
type Runner struct {
	Cfg   *config.Config
	DB    *gorm.DB
	OAuth *oauth.Flow
}

// RunAll triages both inboxes. It returns an error only for fatal setup
// failures; per-account problems are recorded in the run summary.
func (r *Runner) RunAll(ctx context.Context) error {
	log := gutillog.FromContext(ctx)
	if !r.Cfg.Triage.Enabled {
		log.Infow("triage disabled, skipping")
		return nil
	}

	// Advisory locks are session-scoped, so lock and unlock need one conn.
	sqlDB, err := r.DB.DB()
	if err != nil {
		return err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", triageLockKey).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		log.Infow("triage already running, skipping")
		return nil
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", triageLockKey)
	}()

	run := models.AgentRun{
		Kind:      models.AgentRunTriage,
		StartedAt: time.Now(),
		Status:    models.AgentRunRunning,
		Model:     config.VertexModel,
	}
	if err := r.DB.WithContext(ctx).Create(&run).Error; err != nil {
		return err
	}

	counts := map[string]int{}
	var runErrs []string
	tokensIn, tokensOut := r.triageAccounts(ctx, run.ID, counts, &runErrs)

	return r.finish(ctx, run.ID, counts, runErrs, tokensIn, tokensOut)
}

func (r *Runner) triageAccounts(ctx context.Context, runID string, counts map[string]int, runErrs *[]string) (tokensIn, tokensOut int) {
	log := gutillog.FromContext(ctx)

	// Corrections come from decisions Nat has manually reversed. There is no
	// autonomous reconcile pass: detecting reversals would mean inspecting mail
	// Art has already moved out of the inbox, and Art only reads the inbox.
	corrections, err := buildCorrections(ctx, r.DB, r.Cfg.Triage.ReconcileDays, maxCorrections)
	if err != nil {
		log.Warnw("building corrections failed", "err", err)
	}

	classifier, err := NewClassifier(ctx, r.Cfg, corrections)
	if err != nil {
		*runErrs = append(*runErrs, "classifier: "+err.Error())
		return 0, 0
	}

	triager := &Triager{
		DB:                  r.DB,
		Classifier:          classifier,
		BackfillDays:        r.Cfg.Triage.BackfillDays,
		MaxPerRun:           r.Cfg.Triage.MaxPerRun,
		ConfidenceThreshold: r.Cfg.Triage.ConfidenceThreshold,
		DryRun:              r.Cfg.Triage.DryRun,
	}

	// Triage new inbox mail for each linked account.
	for _, kind := range []models.AccountKind{models.AccountPersonal, models.AccountWork} {
		gm, err := gmail.NewClient(ctx, r.OAuth, kind)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				*runErrs = append(*runErrs, fmt.Sprintf("%s: client: %v", kind, err))
			}
			continue // not linked, or failed to build
		}
		n, err := triager.RunAccount(ctx, runID, kind, gm, counts)
		if err != nil {
			*runErrs = append(*runErrs, fmt.Sprintf("%s: %v", kind, err))
		}
		log.Infow("triaged account", "account", kind, "processed", n, "dry_run", r.Cfg.Triage.DryRun)
	}
	return classifier.TokensIn(), classifier.TokensOut()
}

// maxCorrections bounds how many recent reversals feed the classifier prompt.
const maxCorrections = 15

func (r *Runner) finish(ctx context.Context, id string, counts map[string]int, runErrs []string, tokensIn, tokensOut int) error {
	summary := map[string]any{
		"dry_run": r.Cfg.Triage.DryRun,
		"errors":  runErrs,
	}
	for cat, n := range counts {
		summary[cat] = n
	}
	body, _ := json.Marshal(summary)

	status := models.AgentRunSucceeded
	errStr := ""
	if len(runErrs) > 0 {
		status = models.AgentRunFailed
		errStr = runErrs[0]
	}
	t := time.Now()
	// Record the outcome even if ctx already timed out mid-run, so a started
	// run never stays stuck "running".
	return r.DB.WithContext(context.WithoutCancel(ctx)).Model(&models.AgentRun{}).Where("id = ?", id).Updates(map[string]any{
		"ended_at":   &t,
		"status":     string(status),
		"summary":    datatypes.JSON(body),
		"error":      errStr,
		"tokens_in":  tokensIn,
		"tokens_out": tokensOut,
	}).Error
}
