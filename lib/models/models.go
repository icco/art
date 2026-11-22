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

// AgentRunStatus is the lifecycle status of an AgentRun.
type AgentRunStatus string

// AgentRunKind distinguishes the planner from the email triager so both can
// share the agent_runs table.
type AgentRunKind string

// JobKind identifies which recurring task a queued Job runs.
type JobKind string

// JobStatus is the lifecycle status of a Job.
type JobStatus string

// EmailCategory is the triage classification assigned to an email.
type EmailCategory string

// EmailAction is the action art actually applied to an email.
type EmailAction string

// Enum values used as the string representation in Postgres.
const (
	AccountPersonal AccountKind = "personal"
	AccountWork     AccountKind = "work"

	AgentRunRunning   AgentRunStatus = "running"
	AgentRunSucceeded AgentRunStatus = "succeeded"
	AgentRunFailed    AgentRunStatus = "failed"

	AgentRunPlanner   AgentRunKind = "planner"
	AgentRunTriage    AgentRunKind = "triage"
	AgentRunReconcile AgentRunKind = "reconcile"

	JobTriage JobKind = "triage"

	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"

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
	// ActionNone is the column default for a row with no recorded action.
	ActionNone EmailAction = "none"
)

// Valid reports whether a is one of the recognised AccountKind values.
func (a AccountKind) Valid() bool { return a == AccountPersonal || a == AccountWork }

// Valid reports whether k is one of the recognised AgentRunKind values.
func (k AgentRunKind) Valid() bool {
	return k == AgentRunPlanner || k == AgentRunTriage || k == AgentRunReconcile
}

// Valid reports whether k is one of the recognised JobKind values.
func (k JobKind) Valid() bool {
	return k == JobTriage
}

// Valid reports whether s is one of the recognised JobStatus values.
func (s JobStatus) Valid() bool {
	switch s {
	case JobPending, JobRunning, JobSucceeded, JobFailed:
		return true
	}
	return false
}

// JobKinds returns all served job kinds.
func JobKinds() []JobKind { return []JobKind{JobTriage} }

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
	Email                 string      `gorm:"type:varchar(255);not null"                                              json:"email"`
	RefreshTokenEncrypted []byte      `gorm:"type:bytea;not null"                                                     json:"-"`
}

// AgentRun records one triage invocation, its model usage, and
// outcome. Kind discriminates which agent produced the row.
type AgentRun struct {
	Base
	Kind      AgentRunKind   `gorm:"type:varchar(16);not null;default:'triage';index;check:kind IN ('planner','triage','reconcile')" json:"kind"`
	StartedAt time.Time      `gorm:"not null;default:now();index:idx_agent_runs_started"                                             json:"started_at"`
	EndedAt   *time.Time     `json:"ended_at,omitempty"`
	Status    AgentRunStatus `gorm:"type:varchar(16);not null;default:'running';check:status IN ('running','succeeded','failed')"    json:"status"`
	Model     string         `gorm:"type:varchar(64);not null;default:''"                                                            json:"model"`
	TokensIn  int            `gorm:"not null;default:0"                                                                              json:"tokens_in"`
	TokensOut int            `gorm:"not null;default:0"                                                                              json:"tokens_out"`
	Summary   datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'"                                                                json:"summary"`
	Error     string         `gorm:"type:text;not null;default:''"                                                                   json:"error"`
}

// EmailMessage is one triaged Gmail message: its metadata, the classification
// art assigned, the action art applied, and reconcile state used for learning.
// Bodies are never stored; re-fetch from Gmail on demand.
type EmailMessage struct {
	Base
	RunID          string      `gorm:"type:uuid;not null;index"                                                                                    json:"run_id"`
	AccountKind    AccountKind `gorm:"type:varchar(16);not null;uniqueIndex:idx_email_lookup,priority:1;check:account_kind IN ('personal','work')" json:"account_kind"`
	GmailMessageID string      `gorm:"type:varchar(255);not null;uniqueIndex:idx_email_lookup,priority:2"                                          json:"gmail_message_id"`
	ThreadID       string      `gorm:"type:varchar(255);not null;default:''"                                                                       json:"thread_id"`
	FromAddr       string      `gorm:"type:text;not null;default:''"                                                                               json:"from"`
	ToAddr         string      `gorm:"type:text;not null;default:''"                                                                               json:"to"`
	Subject        string      `gorm:"type:text;not null;default:''"                                                                               json:"subject"`
	Snippet        string      `gorm:"type:text;not null;default:''"                                                                               json:"snippet"`
	ReceivedAt     time.Time   `gorm:"index"                                                                                                       json:"received_at"`

	Category   EmailCategory `gorm:"type:varchar(16);not null;check:category IN ('archive','reply','keep')" json:"category"`
	Summary    string        `gorm:"type:text;not null;default:''"                                          json:"summary"`
	Reason     string        `gorm:"type:text;not null;default:''"                                          json:"reason"`
	Confidence float64       `gorm:"type:numeric(4,3);not null;default:0"                                   json:"confidence"`

	Action   EmailAction `gorm:"type:varchar(16);not null;default:'none'" json:"action"`
	Applied  bool        `gorm:"not null;default:false"                   json:"applied"`
	Archived bool        `gorm:"not null;default:false"                   json:"archived"`

	Reversed     bool       `gorm:"not null;default:false;index"         json:"reversed"`
	ReversalKind string     `gorm:"type:varchar(32);not null;default:''" json:"reversal_kind"`
	ReconciledAt *time.Time `json:"reconciled_at,omitempty"`
}

// Job is one unit of queued background work; the partial unique index
// allows only one pending job per kind.
type Job struct {
	Base
	Kind        JobKind    `gorm:"type:varchar(16);not null;check:kind IN ('sync','reconcile','planner','triage');index:idx_jobs_one_pending,unique,where:status = 'pending'" json:"kind"`
	Status      JobStatus  `gorm:"type:varchar(16);not null;default:'pending';index;check:status IN ('pending','running','succeeded','failed')"                               json:"status"`
	RunAt       time.Time  `gorm:"not null;index"                                                                                                                             json:"run_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Attempts    int        `gorm:"not null;default:0"                                                                                                                         json:"attempts"`
	MaxAttempts int        `gorm:"not null;default:4"                                                                                                                         json:"max_attempts"`
	LastError   string     `gorm:"type:text;not null;default:''"                                                                                                              json:"last_error"`
}

// Setting is one runtime-editable configuration value. Keys are an allowlist
// owned by lib/settings; secrets and deploy-time config stay in the env.
type Setting struct {
	Base
	Key   string `gorm:"type:varchar(64);uniqueIndex;not null" json:"key"`
	Value string `gorm:"type:text;not null;default:''"         json:"value"`
}

// All returns the models in AutoMigrate order.
func All() []any {
	return []any{
		&Account{},
		&Setting{},
		&AgentRun{},
		&EmailMessage{},
		&Job{},
	}
}
