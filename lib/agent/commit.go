package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/models"
)

// commitFunc books one focus block: Google event + sessions row. It returns
// the session ID and Google event ID. The deterministic planner takes it as
// a parameter so tests can fake the Google side.
type commitFunc func(ctx context.Context, source models.SourceKind, sourceID string, start, end time.Time) (string, string, error)

// blockWriter commits focus blocks for a Planner, caching one calendar
// client per account across a run.
type blockWriter struct {
	p       *Planner
	clients map[models.AccountKind]*calendar.Client
}

func newBlockWriter(p *Planner) *blockWriter {
	return &blockWriter{p: p, clients: map[models.AccountKind]*calendar.Client{}}
}

// CommitBlock creates a focusTime event on the calendar matching the
// source's kind (work → work account, personal → personal) and records a
// sessions row linking it back to the source.
func (b *blockWriter) CommitBlock(ctx context.Context, source models.SourceKind, sourceID string, start, end time.Time) (string, string, error) {
	name, kind, err := b.resolveSource(ctx, source, sourceID)
	if err != nil {
		return "", "", err
	}

	acct := accountForKind(kind)
	client, err := b.clientFor(ctx, acct)
	if err != nil {
		return "", "", fmt.Errorf("account %s not linked: %w", acct, err)
	}

	calID := client.Account.PrimaryCalendarID
	if client.Account.ArtCalendarID != nil && *client.Account.ArtCalendarID != "" {
		calID = *client.Account.ArtCalendarID
	}
	ev, err := client.CreateFocus(ctx, calendar.FocusBlock{
		CalendarID:  calID,
		Start:       start,
		End:         end,
		Summary:     focusTitle(source, name),
		Description: focusDescription(source, sourceID),
		Source:      source,
		SourceID:    sourceID,
	})
	if err != nil {
		return "", "", err
	}

	sess := models.Session{
		Source:         source,
		SourceID:       sourceID,
		AccountKind:    client.Account.Kind,
		CalendarID:     calID,
		GoogleEventID:  &ev.Id,
		ScheduledStart: start,
		ScheduledEnd:   end,
		Status:         models.SessionPlanned,
	}
	if err := b.p.DB.WithContext(ctx).Create(&sess).Error; err != nil {
		return "", "", err
	}
	return sess.ID, ev.Id, nil
}

func (b *blockWriter) resolveSource(ctx context.Context, source models.SourceKind, id string) (string, models.SlotKind, error) {
	switch source {
	case models.SourceProject:
		var pj models.Project
		if err := b.p.DB.WithContext(ctx).First(&pj, "id = ?", id).Error; err != nil {
			return "", "", fmt.Errorf("project %s: %w", id, err)
		}
		return pj.Name, pj.Kind, nil
	case models.SourceHabit:
		var h models.Habit
		if err := b.p.DB.WithContext(ctx).First(&h, "id = ?", id).Error; err != nil {
			return "", "", fmt.Errorf("habit %s: %w", id, err)
		}
		return h.Name, h.Kind, nil
	case models.SourceTask:
		var task models.Task
		if err := b.p.DB.WithContext(ctx).First(&task, "id = ?", id).Error; err != nil {
			return "", "", fmt.Errorf("task %s: %w", id, err)
		}
		return task.Title, task.Kind, nil
	}
	return "", "", errors.New("unknown source kind")
}

func (b *blockWriter) clientFor(ctx context.Context, acct models.AccountKind) (*calendar.Client, error) {
	if cl, ok := b.clients[acct]; ok {
		return cl, nil
	}
	cl, err := calendar.NewClient(ctx, b.p.OAuth, acct)
	if err != nil {
		return nil, err
	}
	b.clients[acct] = cl
	return cl, nil
}
