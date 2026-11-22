// Package settings stores the runtime-editable subset of art's configuration in
// Postgres. Callers load at use time — once per triage run — so an
// edit takes effect without a redeploy.
package settings

import (
	"context"
	"fmt"
	"strconv"

	"go.icco.me/art/lib/config"
	"go.icco.me/art/lib/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The settable keys. This list is the allowlist: nothing else is read from or
// written to the settings table, so secrets and deploy-time config (owner
// emails, encryption keys, DSNs, OAuth and Vertex config) stay env-only.
const (
	KeyTriageEnabled             = "triage_enabled"
	KeyTriageDryRun              = "triage_dry_run"
	KeyTriageConfidenceThreshold = "triage_confidence_threshold"
	KeyTriageBackfillDays        = "triage_backfill_days"
	KeyTriageReconcileDays       = "triage_reconcile_days"
	KeyDailyBudgetUSD            = "daily_budget_usd"
)

// DefaultDailyBudgetUSD caps what art may spend on Vertex AI per day. Triage is
// the only spender left, and at Flash prices with thinking off it runs about a
// quarter of this — so the ceiling is headroom, not a squeeze.
const DefaultDailyBudgetUSD = 2.0

// maxDailyBudgetUSD is a sanity rail against a fat-fingered setting.
const maxDailyBudgetUSD = 100.0

// maxDays is a sanity rail on the day-count settings.
const maxDays = 365

// settingsLockKey is the Postgres advisory transaction lock for settings updates.
const settingsLockKey = 0x4152545f5354 // "ART_ST"

// Keys returns every settable key.
func Keys() []string {
	return []string{
		KeyTriageEnabled, KeyTriageDryRun, KeyTriageConfidenceThreshold,
		KeyTriageBackfillDays, KeyTriageReconcileDays, KeyDailyBudgetUSD,
	}
}

// Values is the full set of runtime-editable settings.
type Values struct {
	TriageEnabled             bool    `json:"triage_enabled"`
	TriageDryRun              bool    `json:"triage_dry_run"`
	TriageConfidenceThreshold float64 `json:"triage_confidence_threshold"`
	TriageBackfillDays        int     `json:"triage_backfill_days"`
	TriageReconcileDays       int     `json:"triage_reconcile_days"`
	DailyBudgetUSD            float64 `json:"daily_budget_usd"`
}

// Validate rejects values the triager could not act on.
func (v Values) Validate() error {
	if t := v.TriageConfidenceThreshold; t < 0 || t > 1 {
		return fmt.Errorf("%w: triage_confidence_threshold must be in [0, 1], got %v", ErrInvalidSetting, t)
	}
	if d := v.TriageBackfillDays; d < 1 || d > maxDays {
		return fmt.Errorf("%w: triage_backfill_days must be 1-%d, got %d", ErrInvalidSetting, maxDays, d)
	}
	if d := v.TriageReconcileDays; d < 0 || d > maxDays {
		return fmt.Errorf("%w: triage_reconcile_days must be 0-%d, got %d", ErrInvalidSetting, maxDays, d)
	}
	if b := v.DailyBudgetUSD; b < 0 || b > maxDailyBudgetUSD {
		return fmt.Errorf("%w: daily_budget_usd must be 0-%v (0 disables the cap), got %v", ErrInvalidSetting, maxDailyBudgetUSD, b)
	}
	return nil
}

// DefaultsFrom seeds the fallbacks from the env-loaded config, so an
// unwritten setting behaves exactly as it did before this table existed.
// A nil cfg yields the built-in defaults alone.
func DefaultsFrom(cfg *config.Config) Values {
	v := Values{
		TriageEnabled:             true,
		TriageConfidenceThreshold: config.DefaultTriageConfidenceThreshold,
		TriageBackfillDays:        config.DefaultTriageBackfillDays,
		TriageReconcileDays:       config.DefaultTriageReconcileDays,
		DailyBudgetUSD:            DefaultDailyBudgetUSD,
	}
	if cfg == nil {
		return v
	}
	v.TriageEnabled = cfg.Triage.Enabled
	v.TriageDryRun = cfg.Triage.DryRun
	v.TriageConfidenceThreshold = cfg.Triage.ConfidenceThreshold
	v.TriageBackfillDays = cfg.Triage.BackfillDays
	v.TriageReconcileDays = cfg.Triage.ReconcileDays
	return v
}

// Store reads and writes the settings table.
type Store struct {
	DB *gorm.DB
	// Defaults back every key without a row.
	Defaults Values
}

// New returns a Store whose fallbacks come from the env-loaded config.
func New(db *gorm.DB, cfg *config.Config) *Store {
	return &Store{DB: db, Defaults: DefaultsFrom(cfg)}
}

// Load returns the stored settings, falling back to the defaults per key.
// Unparseable values fall back too: the API is the only writer, so a bad row is
// corruption and the triager should keep running on the last known-good value.
func (s *Store) Load(ctx context.Context) (Values, error) {
	out := s.Defaults
	var rows []models.Setting
	if err := s.DB.WithContext(ctx).Where("key IN ?", Keys()).Find(&rows).Error; err != nil {
		return out, err
	}
	for _, row := range rows {
		apply(&out, row.Key, row.Value)
	}
	return out, nil
}

// Save validates vals and upserts every settable key.
func (s *Store) Save(ctx context.Context, vals Values) error {
	if err := vals.Validate(); err != nil {
		return err
	}
	rows := encode(vals)
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", settingsLockKey).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		}).Create(&rows).Error
	})
}

// Update applies fn to the stored settings, validates, and saves them in one
// locked transaction so concurrent updates cannot overwrite each other.
func (s *Store) Update(ctx context.Context, fn func(v *Values) error) (Values, error) {
	var result Values
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", settingsLockKey).Error; err != nil {
			return err
		}
		out, err := s.loadLocked(ctx, tx)
		if err != nil {
			return err
		}
		if fn != nil {
			if err := fn(&out); err != nil {
				return err
			}
		}
		if err := out.Validate(); err != nil {
			return err
		}
		rows := encode(out)
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
		}).Create(&rows).Error; err != nil {
			return err
		}
		result = out
		return nil
	})
	return result, err
}

func (s *Store) loadLocked(ctx context.Context, tx *gorm.DB) (Values, error) {
	out := s.Defaults
	var rows []models.Setting
	if err := tx.WithContext(ctx).Where("key IN ?", Keys()).Find(&rows).Error; err != nil {
		return out, err
	}
	for _, row := range rows {
		apply(&out, row.Key, row.Value)
	}
	return out, nil
}

func apply(v *Values, key, raw string) {
	switch key {
	case KeyTriageEnabled:
		applyBool(&v.TriageEnabled, raw)
	case KeyTriageDryRun:
		applyBool(&v.TriageDryRun, raw)
	case KeyTriageConfidenceThreshold:
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			v.TriageConfidenceThreshold = f
		}
	case KeyTriageBackfillDays:
		applyInt(&v.TriageBackfillDays, raw)
	case KeyTriageReconcileDays:
		applyInt(&v.TriageReconcileDays, raw)
	case KeyDailyBudgetUSD:
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			v.DailyBudgetUSD = f
		}
	}
}

func applyBool(dst *bool, raw string) {
	if b, err := strconv.ParseBool(raw); err == nil {
		*dst = b
	}
}

func applyInt(dst *int, raw string) {
	if n, err := strconv.Atoi(raw); err == nil {
		*dst = n
	}
}

func encode(v Values) []models.Setting {
	vals := map[string]string{
		KeyTriageEnabled:             strconv.FormatBool(v.TriageEnabled),
		KeyTriageDryRun:              strconv.FormatBool(v.TriageDryRun),
		KeyTriageConfidenceThreshold: strconv.FormatFloat(v.TriageConfidenceThreshold, 'f', -1, 64),
		KeyTriageBackfillDays:        strconv.Itoa(v.TriageBackfillDays),
		KeyTriageReconcileDays:       strconv.Itoa(v.TriageReconcileDays),
		KeyDailyBudgetUSD:            strconv.FormatFloat(v.DailyBudgetUSD, 'f', -1, 64),
	}
	rows := make([]models.Setting, 0, len(vals))
	for _, k := range Keys() {
		rows = append(rows, models.Setting{Key: k, Value: vals[k]})
	}
	return rows
}
