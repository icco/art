package handlers

import (
	"context"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
)

// plannerRunTimeout bounds a detached planner pass and doubles as the
// staleness window for the in-flight guard, mirroring triage.
const plannerRunTimeout = 30 * time.Minute

// ReplanRun starts a planner pass and returns immediately. A pass is a
// multi-turn LLM run that outlives the server's write timeout, so it runs
// detached; clients poll /agent-runs (kind=planner) for the outcome.
func (h *Handlers) ReplanRun(w http.ResponseWriter, r *http.Request) {
	var running int64
	if err := h.DB.WithContext(r.Context()).Model(&models.AgentRun{}).
		Where("kind = ? AND status = ? AND started_at > ?",
			string(models.AgentRunPlanner), string(models.AgentRunRunning),
			time.Now().Add(-plannerRunTimeout)).
		Count(&running).Error; err != nil {
		writeServerError(w, r, "replan run", err)
		return
	}
	if running > 0 {
		writeJSON(w, r, http.StatusAccepted, map[string]any{"status": "running"})
		return
	}

	ctx := context.WithoutCancel(r.Context())
	go func() {
		defer func() {
			if p := recover(); p != nil {
				gutillog.FromContext(ctx).Errorw("planner run panicked",
					"panic", p, "stack", string(debug.Stack()))
			}
		}()
		ctx, cancel := context.WithTimeout(ctx, plannerRunTimeout)
		defer cancel()
		if err := h.Planner.Run(ctx); err != nil {
			gutillog.FromContext(ctx).Errorw("planner run", "err", err)
		}
	}()
	writeJSON(w, r, http.StatusAccepted, map[string]any{"status": "started"})
}
