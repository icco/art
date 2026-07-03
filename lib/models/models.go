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

// SourceKind says whether a Session was generated from a Project or Habit.
type SourceKind string

// SessionStatus is the lifecycle status of a Session.
type SessionStatus string

// AgentRunStatus is the lifecycle status of an AgentRun.
type AgentRunStatus string

// AgentRunKind distinguishes the planner from the email triager so both can
// share the agent_runs table.
type AgentRunKind string

// EmailCategory is the triage classification assigned to an email.
type EmailCategory string

// EmailAction is the action art actually applied to an email.
type EmailAction string

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

	SessionPlanned  SessionStatus = "planned"
	SessionHappened SessionStatus = "happened"
	SessionSkipped  SessionStatus = "skipped"
	SessionMoved    SessionStatus = "moved"

	AgentRunRunning   AgentRunStatus = "running"
	AgentRunSucceeded AgentRunStatus = "succeeded"
	AgentRunFailed    AgentRunStatus = "failed"

	AgentRunPlanner AgentRunKind = "planner"
	AgentRunTriage  AgentRunKind = "triage"

	// EmailArchive marks bulk mail to remove from the inbox.
	EmailArchive EmailCategory = "archive"
	// EmailReply marks mail that wants a response; art labels it Art/Reply for
	// Nat to handle and never writes the reply itself.
	EmailReply EmailCategory = "reply"
	// EmailKeep marks mail art leaves untouched in the inbox.
	EmailKeep EmailCategory = "keep"

	// ActionArchived means art removed INBOX and added Art/Archived.
	ActionArchived EmailAction = "archived"
	// ActionReply means art labeled Art/Reply so Nat can respond.
	ActionReply EmailAction = "reply"
	// ActionKeep means art only labeled Art/Triaged.
	ActionKeep EmailAction = "keep"
	// ActionNone means art classified but applied nothing (dry run).
	ActionNone EmailAction = "none"
)

// Valid reports whether a is one of the recognised AccountKind values.
func (a AccountKind) Valid() bool { return a == AccountPersonal || a == AccountWork }

// Valid reports whether s is one of the recognised SlotKind values.
func (s SlotKind) Valid() bool { return s == SlotWork || s == SlotPersonal }

// Valid reports whether s is one of the recognised SourceKind values.
func (s SourceKind) Valid() bool { return s == SourceProject || s == SourceHabit }

// Valid reports whether k is one of the recognised AgentRunKind values.
func (k AgentRunKind) Valid() bool { return k == AgentRunPlanner || k == AgentRunTriage }

// Valid reports whether c is one of the recognised EmailCategory values.
func (c EmailCategory) Valid() bool {
	switch c {
	case EmailArchive, EmailReply, EmailKeep:
		return true
	}
	return false
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

// Valid reports whether the cadence uses a type the planner understands and a
// positive count; anything else silently degrades to wrong scheduling.
func (c Cadence) Valid() bool {
	switch c.Type {
	case "per_week", "per_day":
		return c.Count > 0
	}
	return false
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

// Session is one planned or completed instance of a project or habit on the calendar.
type Session struct {
	Base
	Source         SourceKind    `gorm:"type:varchar(16);not null;check:source IN ('project','habit');index:idx_session_source,priority:1" json:"source"`
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

// AgentRun records one planner or triage invocation, its model usage, and
// outcome. Kind discriminates which agent produced the row.
type AgentRun struct {
	Base
	Kind      AgentRunKind   `gorm:"type:varchar(16);not null;default:'planner';index;check:kind IN ('planner','triage')" json:"kind"`
	StartedAt time.Time      `gorm:"not null;default:now();index:idx_agent_runs_started" json:"started_at"`
	EndedAt   *time.Time     `json:"ended_at,omitempty"`
	Status    AgentRunStatus `gorm:"type:varchar(16);not null;default:'running';check:status IN ('running','succeeded','failed')" json:"status"`
	Model     string         `gorm:"type:varchar(64);not null;default:''" json:"model"`
	TokensIn  int            `gorm:"not null;default:0" json:"tokens_in"`
	TokensOut int            `gorm:"not null;default:0" json:"tokens_out"`
	Summary   datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"summary"`
	Error     string         `gorm:"type:text;not null;default:''" json:"error"`
}

// EmailMessage is one triaged Gmail message: its metadata, the classification
// art assigned, the action art applied, and reconcile state used for learning.
// Bodies are never stored; re-fetch from Gmail on demand.
type EmailMessage struct {
	Base
	RunID          string      `gorm:"type:uuid;not null;index" json:"run_id"`
	AccountKind    AccountKind `gorm:"type:varchar(16);not null;uniqueIndex:idx_email_lookup,priority:1;check:account_kind IN ('personal','work')" json:"account_kind"`
	GmailMessageID string      `gorm:"type:varchar(255);not null;uniqueIndex:idx_email_lookup,priority:2" json:"gmail_message_id"`
	ThreadID       string      `gorm:"type:varchar(255);not null;default:''" json:"thread_id"`
	FromAddr       string      `gorm:"type:text;not null;default:''" json:"from"`
	ToAddr         string      `gorm:"type:text;not null;default:''" json:"to"`
	Subject        string      `gorm:"type:text;not null;default:''" json:"subject"`
	Snippet        string      `gorm:"type:text;not null;default:''" json:"snippet"`
	ReceivedAt     time.Time   `gorm:"index" json:"received_at"`

	Category   EmailCategory `gorm:"type:varchar(16);not null;check:category IN ('archive','reply','keep')" json:"category"`
	Summary    string        `gorm:"type:text;not null;default:''" json:"summary"`
	Reason     string        `gorm:"type:text;not null;default:''" json:"reason"`
	Confidence float64       `gorm:"type:numeric(4,3);not null;default:0" json:"confidence"`

	Action   EmailAction `gorm:"type:varchar(16);not null;default:'none'" json:"action"`
	Applied  bool        `gorm:"not null;default:false" json:"applied"`
	Archived bool        `gorm:"not null;default:false" json:"archived"`

	Reversed     bool       `gorm:"not null;default:false;index" json:"reversed"`
	ReversalKind string     `gorm:"type:varchar(32);not null;default:''" json:"reversal_kind"`
	ReconciledAt *time.Time `json:"reconciled_at,omitempty"`
}

// All returns the models in AutoMigrate order.
func All() []any {
	return []any{
		&Account{},
		&WorkingHour{},
		&Project{},
		&Habit{},
		&Session{},
		&Event{},
		&SyncState{},
		&AgentRun{},
		&EmailMessage{},
	}
}
