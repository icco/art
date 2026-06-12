// Package models defines the GORM schema. AutoMigrate creates the tables.
package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// The string types below are enum-like values stored as Postgres varchar.
// Postgres ENUMs are avoided because AutoMigrate doesn't manage them well;
// CHECK constraints in the tags enforce the allowed values instead.

// AccountKind identifies which linked Google account an entity belongs to.
type AccountKind string

// SlotKind tags whether something is considered work or personal time.
type SlotKind string

// ProjectStatus is the lifecycle status of a Project.
type ProjectStatus string

// SourceKind says whether a Session was generated from a Project, Habit, or Task.
type SourceKind string

// TaskStatus is the lifecycle status of a Task.
type TaskStatus string

// SessionStatus is the lifecycle status of a Session.
type SessionStatus string

// AgentRunStatus is the lifecycle status of an AgentRun.
type AgentRunStatus string

// Enum values used as the string representation in Postgres.
const (
	AccountPersonal AccountKind = "personal"
	AccountWork     AccountKind = "work"

	SlotWork     SlotKind = "work"
	SlotPersonal SlotKind = "personal"

	ProjectActive ProjectStatus = "active"
	ProjectPaused ProjectStatus = "paused"
	ProjectDone   ProjectStatus = "done"

	SourceProject SourceKind = "project"
	SourceHabit   SourceKind = "habit"
	SourceTask    SourceKind = "task"

	TaskPending       TaskStatus = "pending"
	TaskScheduled     TaskStatus = "scheduled"
	TaskDone          TaskStatus = "done"
	TaskUnschedulable TaskStatus = "unschedulable"

	SessionPlanned  SessionStatus = "planned"
	SessionHappened SessionStatus = "happened"
	SessionSkipped  SessionStatus = "skipped"
	SessionMoved    SessionStatus = "moved"

	AgentRunRunning   AgentRunStatus = "running"
	AgentRunSucceeded AgentRunStatus = "succeeded"
	AgentRunFailed    AgentRunStatus = "failed"
)

// Valid reports whether a is one of the recognised AccountKind values.
func (a AccountKind) Valid() bool { return a == AccountPersonal || a == AccountWork }

// Valid reports whether s is one of the recognised SlotKind values.
func (s SlotKind) Valid() bool { return s == SlotWork || s == SlotPersonal }

// Valid reports whether s is one of the recognised SourceKind values.
func (s SourceKind) Valid() bool {
	return s == SourceProject || s == SourceHabit || s == SourceTask
}

// Valid reports whether s is one of the recognised TaskStatus values.
func (s TaskStatus) Valid() bool {
	return s == TaskPending || s == TaskScheduled || s == TaskDone || s == TaskUnschedulable
}

// Base is embedded into every GORM model and supplies a UUID primary key
// along with created/updated timestamps managed by GORM.
type Base struct {
	ID        string    `gorm:"type:uuid;primaryKey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BeforeCreate fills the UUID in Go so we don't need a Postgres extension.
func (b *Base) BeforeCreate(_ *gorm.DB) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	return nil
}

// Account is a single linked Google account (personal or work).
type Account struct {
	Base
	Kind                  AccountKind `gorm:"type:varchar(16);uniqueIndex;not null;check:kind IN ('personal','work')" json:"kind"`
	Email                 string      `gorm:"type:varchar(255);not null" json:"email"`
	RefreshTokenEncrypted []byte      `gorm:"type:bytea;not null" json:"-"`
	PrimaryCalendarID     string      `gorm:"type:varchar(255);not null" json:"primary_calendar_id"`
	ArtCalendarID         *string     `gorm:"type:varchar(255)" json:"art_calendar_id,omitempty"`
}

// WorkingHour is one allowed-time window for a given slot kind and weekday.
type WorkingHour struct {
	Base
	SlotKind    SlotKind `gorm:"type:varchar(16);not null;check:slot_kind IN ('work','personal');uniqueIndex:idx_wh_unique,priority:1" json:"slot_kind"`
	DayOfWeek   int      `gorm:"not null;check:day_of_week BETWEEN 0 AND 6;uniqueIndex:idx_wh_unique,priority:2" json:"day_of_week"`
	StartMinute int      `gorm:"not null;check:start_minute BETWEEN 0 AND 1439;uniqueIndex:idx_wh_unique,priority:3" json:"start_minute"`
	EndMinute   int      `gorm:"not null;check:end_minute BETWEEN 1 AND 1440" json:"end_minute"`
}

// Project is a goal with a target number of hours and an optional deadline.
type Project struct {
	Base
	Name           string        `gorm:"type:varchar(255);not null" json:"name"`
	Description    string        `gorm:"type:text;not null;default:''" json:"description"`
	Kind           SlotKind      `gorm:"type:varchar(16);not null;index;check:kind IN ('work','personal')" json:"kind"`
	TargetHours    float64       `gorm:"type:numeric(6,2);not null" json:"target_hours"`
	ScheduledHours float64       `gorm:"type:numeric(6,2);not null;default:0" json:"scheduled_hours"`
	Deadline       *time.Time    `json:"deadline,omitempty"`
	Status         ProjectStatus `gorm:"type:varchar(16);not null;default:'active';index;check:status IN ('active','paused','done')" json:"status"`
}

// Cadence is the JSONB payload on Habit.Cadence.
type Cadence struct {
	Type             string   `json:"type"`
	Count            int      `json:"count"`
	PreferredWindows []string `json:"preferred_windows,omitempty"`
}

// Habit is a recurring practice with a cadence and per-block duration.
type Habit struct {
	Base
	Name                 string         `gorm:"type:varchar(255);not null" json:"name"`
	Description          string         `gorm:"type:text;not null;default:''" json:"description"`
	Kind                 SlotKind       `gorm:"type:varchar(16);not null;index;check:kind IN ('work','personal')" json:"kind"`
	BlockDurationMinutes int            `gorm:"not null;check:block_duration_minutes > 0" json:"block_duration_minutes"`
	Cadence              datatypes.JSON `gorm:"type:jsonb;not null" json:"cadence"`
	Active               bool           `gorm:"not null;default:true;index" json:"active"`
}

// Task is a one-off piece of work to schedule once, then complete.
type Task struct {
	Base
	Title           string     `gorm:"type:varchar(255);not null" json:"title"`
	Kind            SlotKind   `gorm:"type:varchar(16);not null;index;check:kind IN ('work','personal')" json:"kind"`
	DurationMinutes int        `gorm:"not null;check:duration_minutes > 0" json:"duration_minutes"`
	Deadline        *time.Time `json:"deadline,omitempty"`
	Status          TaskStatus `gorm:"type:varchar(16);not null;default:'pending';index;check:status IN ('pending','scheduled','done','unschedulable')" json:"status"`
	Notes           string     `gorm:"type:text;not null;default:''" json:"notes"`
}

// Session is one planned or completed instance of a project, habit, or task on the calendar.
type Session struct {
	Base
	Source         SourceKind    `gorm:"type:varchar(16);not null;check:source IN ('project','habit','task');index:idx_session_source,priority:1" json:"source"`
	SourceID       string        `gorm:"type:uuid;not null;index:idx_session_source,priority:2" json:"source_id"`
	AccountKind    AccountKind   `gorm:"type:varchar(16);not null;check:account_kind IN ('personal','work')" json:"account_kind"`
	CalendarID     string        `gorm:"type:varchar(255);not null" json:"calendar_id"`
	GoogleEventID  *string       `gorm:"type:varchar(255);uniqueIndex:idx_session_google_event" json:"google_event_id,omitempty"`
	ScheduledStart time.Time     `gorm:"not null;index" json:"scheduled_start"`
	ScheduledEnd   time.Time     `gorm:"not null" json:"scheduled_end"`
	ActualStart    *time.Time    `json:"actual_start,omitempty"`
	ActualEnd      *time.Time    `json:"actual_end,omitempty"`
	Status         SessionStatus `gorm:"type:varchar(16);not null;default:'planned';check:status IN ('planned','happened','skipped','moved')" json:"status"`
}

// Event mirrors a Google Calendar event pulled into the local database.
type Event struct {
	Base
	AccountKind        AccountKind    `gorm:"type:varchar(16);not null;uniqueIndex:idx_event_lookup,priority:1;index:idx_event_window,priority:1" json:"account_kind"`
	CalendarID         string         `gorm:"type:varchar(255);not null;uniqueIndex:idx_event_lookup,priority:2" json:"calendar_id"`
	GoogleEventID      string         `gorm:"type:varchar(255);not null;uniqueIndex:idx_event_lookup,priority:3" json:"google_event_id"`
	Summary            string         `gorm:"type:text;not null;default:''" json:"summary"`
	Description        string         `gorm:"type:text;not null;default:''" json:"description"`
	StartTime          time.Time      `gorm:"not null;index:idx_event_window,priority:2" json:"start_time"`
	EndTime            time.Time      `gorm:"not null" json:"end_time"`
	AllDay             bool           `gorm:"not null;default:false" json:"all_day"`
	AttendeeCount      int            `gorm:"not null;default:0" json:"attendee_count"`
	EventType          string         `gorm:"type:varchar(32);not null;default:'default'" json:"event_type"`
	IsArtManaged       bool           `gorm:"not null;default:false;index" json:"is_art_managed"`
	Status             string         `gorm:"type:varchar(32);not null;default:'confirmed'" json:"status"`
	ExtendedProperties datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"extended_properties"`
}

// SyncState tracks the per-calendar sync token used for incremental syncs.
type SyncState struct {
	AccountKind   AccountKind `gorm:"type:varchar(16);primaryKey" json:"account_kind"`
	CalendarID    string      `gorm:"type:varchar(255);primaryKey" json:"calendar_id"`
	LastSyncToken *string     `gorm:"type:text" json:"last_sync_token,omitempty"`
	LastSyncedAt  *time.Time  `json:"last_synced_at,omitempty"`
}

// AgentRun records one planner invocation, its model usage, and outcome.
type AgentRun struct {
	Base
	StartedAt time.Time      `gorm:"not null;default:now();index:idx_agent_runs_started" json:"started_at"`
	EndedAt   *time.Time     `json:"ended_at,omitempty"`
	Status    AgentRunStatus `gorm:"type:varchar(16);not null;default:'running';check:status IN ('running','succeeded','failed')" json:"status"`
	Model     string         `gorm:"type:varchar(64);not null;default:''" json:"model"`
	TokensIn  int            `gorm:"not null;default:0" json:"tokens_in"`
	TokensOut int            `gorm:"not null;default:0" json:"tokens_out"`
	Summary   datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"summary"`
	Error     string         `gorm:"type:text;not null;default:''" json:"error"`
}

// All returns the models in AutoMigrate order.
func All() []any {
	return []any{
		&Account{},
		&WorkingHour{},
		&Project{},
		&Habit{},
		&Task{},
		&Session{},
		&Event{},
		&SyncState{},
		&AgentRun{},
	}
}
