package handlers

import (
	"net/http"

	"github.com/icco/art/lib/settings"
)

// settingsReq is the writable surface of the settings resource. It doubles as
// the allowlist: decodeJSON rejects unknown fields, so no other key — least of
// all a secret — can reach the store. A nil field keeps its current value.
type settingsReq struct {
	SoftEventTitles           *[]string `json:"soft_event_titles"`
	TriageEnabled             *bool     `json:"triage_enabled"`
	TriageDryRun              *bool     `json:"triage_dry_run"`
	TriageConfidenceThreshold *float64  `json:"triage_confidence_threshold"`
	TriageBackfillDays        *int      `json:"triage_backfill_days"`
	TriageReconcileDays       *int      `json:"triage_reconcile_days"`
	PlanHorizonDays           *int      `json:"plan_horizon_days"`
	FocusBlockMinMinutes      *int      `json:"focus_block_min_minutes"`
	FocusBlockMaxMinutes      *int      `json:"focus_block_max_minutes"`
	DailyBudgetUSD            *float64  `json:"daily_budget_usd"`
}

func (req settingsReq) apply(v *settings.Values) {
	assign(&v.SoftEventTitles, req.SoftEventTitles)
	assign(&v.TriageEnabled, req.TriageEnabled)
	assign(&v.TriageDryRun, req.TriageDryRun)
	assign(&v.TriageConfidenceThreshold, req.TriageConfidenceThreshold)
	assign(&v.TriageBackfillDays, req.TriageBackfillDays)
	assign(&v.TriageReconcileDays, req.TriageReconcileDays)
	assign(&v.PlanHorizonDays, req.PlanHorizonDays)
	assign(&v.FocusBlockMinMinutes, req.FocusBlockMinMinutes)
	assign(&v.FocusBlockMaxMinutes, req.FocusBlockMaxMinutes)
	assign(&v.DailyBudgetUSD, req.DailyBudgetUSD)
}

func assign[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

// SettingsGet responds with the runtime-editable settings. Timezone, owner
// emails, keys and credentials are deploy-time env config and never appear here.
func (h *Handlers) SettingsGet(w http.ResponseWriter, r *http.Request) {
	vals, err := h.Settings.Load(r.Context())
	if err != nil {
		writeServerError(w, r, "settings load", err)
		return
	}
	writeJSON(w, r, http.StatusOK, vals)
}

// SettingsUpdate merges the request into the stored settings and responds with
// the result. Absent fields are left alone.
func (h *Handlers) SettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req settingsReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	vals, err := h.Settings.Load(r.Context())
	if err != nil {
		writeServerError(w, r, "settings load", err)
		return
	}
	req.apply(&vals)
	if err := vals.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Settings.Save(r.Context(), vals); err != nil {
		writeServerError(w, r, "settings save", err)
		return
	}
	writeJSON(w, r, http.StatusOK, vals)
}
