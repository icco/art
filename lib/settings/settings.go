// Package settings stores the runtime-editable subset of art's configuration in
// Postgres. Callers load at use time — once per planner or triage run — so an
// edit takes effect without a redeploy.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The settable keys. This list is the allowlist: nothing else is read from or
// written to the settings table, so secrets and deploy-time config (owner
// emails, encryption keys, DSNs, OAuth and Vertex config) stay env-only.
const (
	KeySoftEventTitles           = "soft_event_titles"
	KeyTriageEnabled             = "triage_enabled"
	KeyTriageDryRun              = "triage_dry_run"
	KeyTriageConfidenceThreshold = "triage_confidence_threshold"
	KeyTriageBackfillDays        = "triage_backfill_days"
	KeyTriageReconcileDays       = "triage_reconcile_days"
	KeyPlanHorizonDays           = "plan_horizon_days"
	KeyFocusBlockMinMinutes      = "focus_block_min_minutes"
	KeyFocusBlockMaxMinutes      = "focus_block_max_minutes"
)

// Defaults for the planner knobs, which have no env var of their own.
const (
	DefaultPlanHorizonDays      = 30
	DefaultFocusBlockMinMinutes = 30
	DefaultFocusBlockMaxMinutes = 90
)

// Accepted ranges. The upper bounds are sanity rails, not policy: a horizon of
// years would make every planner run scan the whole calendar.
const (
	maxDays         = 365
	minBlockMinutes = 5
	maxBlockMinutes = 8 * 60
)

// maxPlanHorizonDays keeps the plan window inside the synced events mirror
// (calendar.FutureWindow). Past the mirror loadBusy finds nothing, so every slot
// looks free and the planner books over real meetings it cannot see — and
// reconcile's window never revisits those sessions to retract them. The day of
// slack absorbs the sync cadence and PlanningStart's rounding to the next hour.
const maxPlanHorizonDays = int(calendar.FutureWindow/(24*time.Hour)) - 1

// Keys returns every settable key.
func Keys() []string {
	return []string{
		KeySoftEventTitles, KeyTriageEnabled, KeyTriageDryRun,
		KeyTriageConfidenceThreshold, KeyTriageBackfillDays, KeyTriageReconcileDays,
		KeyPlanHorizonDays, KeyFocusBlockMinMinutes, KeyFocusBlockMaxMinutes,
	}
}

// Values is the full set of runtime-editable settings.
type Values struct {
	SoftEventTitles           []string `json:"soft_event_titles"`
	TriageEnabled             bool     `json:"triage_enabled"`
	TriageDryRun              bool     `json:"triage_dry_run"`
	TriageConfidenceThreshold float64  `json:"triage_confidence_threshold"`
	TriageBackfillDays        int      `json:"triage_backfill_days"`
	TriageReconcileDays       int      `json:"triage_reconcile_days"`
	PlanHorizonDays           int      `json:"plan_horizon_days"`
	FocusBlockMinMinutes      int      `json:"focus_block_min_minutes"`
	FocusBlockMaxMinutes      int      `json:"focus_block_max_minutes"`
}

// SoftTitles normalizes the soft-event titles for matching.
func (v Values) SoftTitles() models.SoftTitles {
	return models.NewSoftTitles(v.SoftEventTitles...)
}

// PlanHorizon is how far ahead the planner may schedule.
func (v Values) PlanHorizon() time.Duration {
	return time.Duration(v.PlanHorizonDays) * 24 * time.Hour
}

// Validate rejects values the planner or triager could not act on.
func (v Values) Validate() error {
	if t := v.TriageConfidenceThreshold; t < 0 || t > 1 {
		return fmt.Errorf("triage_confidence_threshold must be in [0, 1], got %v", t)
	}
	if d := v.TriageBackfillDays; d < 1 || d > maxDays {
		return fmt.Errorf("triage_backfill_days must be 1-%d, got %d", maxDays, d)
	}
	if d := v.TriageReconcileDays; d < 0 || d > maxDays {
		return fmt.Errorf("triage_reconcile_days must be 0-%d, got %d", maxDays, d)
	}
	if d := v.PlanHorizonDays; d < 1 || d > maxPlanHorizonDays {
		return fmt.Errorf("plan_horizon_days must be 1-%d (the calendar mirror reaches no further), got %d",
			maxPlanHorizonDays, d)
	}
	lo, hi := v.FocusBlockMinMinutes, v.FocusBlockMaxMinutes
	if lo < minBlockMinutes || lo > maxBlockMinutes {
		return fmt.Errorf("focus_block_min_minutes must be %d-%d, got %d", minBlockMinutes, maxBlockMinutes, lo)
	}
	if hi < minBlockMinutes || hi > maxBlockMinutes {
		return fmt.Errorf("focus_block_max_minutes must be %d-%d, got %d", minBlockMinutes, maxBlockMinutes, hi)
	}
	if lo > hi {
		return fmt.Errorf("focus_block_min_minutes (%d) must not exceed focus_block_max_minutes (%d)", lo, hi)
	}
	for _, t := range v.SoftEventTitles {
		if strings.TrimSpace(t) == "" {
			return errors.New("soft_event_titles must not contain blank entries")
		}
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
		PlanHorizonDays:           DefaultPlanHorizonDays,
		FocusBlockMinMinutes:      DefaultFocusBlockMinMinutes,
		FocusBlockMaxMinutes:      DefaultFocusBlockMaxMinutes,
	}
	if cfg == nil {
		return v
	}
	v.SoftEventTitles = cfg.SoftEventTitles
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
// corruption and the planner should keep running on the last known-good value.
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

// SoftTitles loads only the soft-event titles, normalized for matching.
func (s *Store) SoftTitles(ctx context.Context) (models.SoftTitles, error) {
	v, err := s.Load(ctx)
	return v.SoftTitles(), err
}

// Save validates vals and upserts every settable key.
func (s *Store) Save(ctx context.Context, vals Values) error {
	if err := vals.Validate(); err != nil {
		return err
	}
	rows, err := encode(vals)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&rows).Error
}

func apply(v *Values, key, raw string) {
	switch key {
	case KeySoftEventTitles:
		var titles []string
		if json.Unmarshal([]byte(raw), &titles) == nil {
			v.SoftEventTitles = titles
		}
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
	case KeyPlanHorizonDays:
		applyInt(&v.PlanHorizonDays, raw)
	case KeyFocusBlockMinMinutes:
		applyInt(&v.FocusBlockMinMinutes, raw)
	case KeyFocusBlockMaxMinutes:
		applyInt(&v.FocusBlockMaxMinutes, raw)
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

func encode(v Values) ([]models.Setting, error) {
	titles, err := json.Marshal(v.SoftEventTitles)
	if err != nil {
		return nil, err
	}
	vals := map[string]string{
		KeySoftEventTitles:           string(titles),
		KeyTriageEnabled:             strconv.FormatBool(v.TriageEnabled),
		KeyTriageDryRun:              strconv.FormatBool(v.TriageDryRun),
		KeyTriageConfidenceThreshold: strconv.FormatFloat(v.TriageConfidenceThreshold, 'f', -1, 64),
		KeyTriageBackfillDays:        strconv.Itoa(v.TriageBackfillDays),
		KeyTriageReconcileDays:       strconv.Itoa(v.TriageReconcileDays),
		KeyPlanHorizonDays:           strconv.Itoa(v.PlanHorizonDays),
		KeyFocusBlockMinMinutes:      strconv.Itoa(v.FocusBlockMinMinutes),
		KeyFocusBlockMaxMinutes:      strconv.Itoa(v.FocusBlockMaxMinutes),
	}
	rows := make([]models.Setting, 0, len(vals))
	for _, k := range Keys() {
		rows = append(rows, models.Setting{Key: k, Value: vals[k]})
	}
	return rows, nil
}
