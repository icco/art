package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// upcomingBlock is one planned/moved session enriched with its source name,
// so the CLI can render a schedule in a single round trip.
type upcomingBlock struct {
	SessionID   string    `json:"session_id"`
	Source      string    `json:"source"`
	SourceID    string    `json:"source_id"`
	Title       string    `json:"title"`
	AccountKind string    `json:"account_kind"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Status      string    `json:"status"`
}

type statusResponse struct {
	Upcoming           []upcomingBlock  `json:"upcoming"`
	TasksPending       []models.Task    `json:"tasks_pending"`
	TasksUnschedulable []models.Task    `json:"tasks_unschedulable"`
	LastRun            *models.AgentRun `json:"last_run,omitempty"`
}

// Status responds with everything `art status` needs: upcoming blocks with
// source names, open tasks, and the last planner run.
func (h *Handlers) Status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()

	var sessions []models.Session
	if err := h.DB.WithContext(ctx).
		Where("status IN ? AND scheduled_end > ?",
			[]models.SessionStatus{models.SessionPlanned, models.SessionMoved}, now).
		Order("scheduled_start ASC").Limit(100).
		Find(&sessions).Error; err != nil {
		writeServerError(w, r, "status sessions", err)
		return
	}

	names, err := h.sourceNames(ctx, sessions)
	if err != nil {
		writeServerError(w, r, "status source names", err)
		return
	}
	resp := statusResponse{Upcoming: []upcomingBlock{}}
	for _, s := range sessions {
		resp.Upcoming = append(resp.Upcoming, upcomingBlock{
			SessionID:   s.ID,
			Source:      string(s.Source),
			SourceID:    s.SourceID,
			Title:       names[s.SourceID],
			AccountKind: string(s.AccountKind),
			Start:       s.ScheduledStart,
			End:         s.ScheduledEnd,
			Status:      string(s.Status),
		})
	}

	for _, q := range []struct {
		status models.TaskStatus
		dst    *[]models.Task
	}{
		{models.TaskPending, &resp.TasksPending},
		{models.TaskUnschedulable, &resp.TasksUnschedulable},
	} {
		if err := h.DB.WithContext(ctx).
			Where("status = ?", q.status).
			Order("COALESCE(deadline, now() + interval '365 days') ASC").
			Find(q.dst).Error; err != nil {
			writeServerError(w, r, "status tasks", err)
			return
		}
	}

	var run models.AgentRun
	err = h.DB.WithContext(ctx).Order("started_at DESC").First(&run).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
	case err != nil:
		writeServerError(w, r, "status last run", err)
		return
	default:
		resp.LastRun = &run
	}

	writeJSON(w, r, http.StatusOK, resp)
}

// sourceNames resolves session source IDs to their display names in three
// batched queries.
func (h *Handlers) sourceNames(ctx context.Context, sessions []models.Session) (map[string]string, error) {
	ids := map[models.SourceKind][]string{}
	for _, s := range sessions {
		ids[s.Source] = append(ids[s.Source], s.SourceID)
	}
	names := map[string]string{}

	if len(ids[models.SourceTask]) > 0 {
		var tasks []models.Task
		if err := h.DB.WithContext(ctx).Where("id IN ?", ids[models.SourceTask]).Find(&tasks).Error; err != nil {
			return nil, err
		}
		for _, t := range tasks {
			names[t.ID] = t.Title
		}
	}
	if len(ids[models.SourceProject]) > 0 {
		var projects []models.Project
		if err := h.DB.WithContext(ctx).Where("id IN ?", ids[models.SourceProject]).Find(&projects).Error; err != nil {
			return nil, err
		}
		for _, p := range projects {
			names[p.ID] = p.Name
		}
	}
	if len(ids[models.SourceHabit]) > 0 {
		var habits []models.Habit
		if err := h.DB.WithContext(ctx).Where("id IN ?", ids[models.SourceHabit]).Find(&habits).Error; err != nil {
			return nil, err
		}
		for _, hb := range habits {
			names[hb.ID] = hb.Name
		}
	}
	return names, nil
}
