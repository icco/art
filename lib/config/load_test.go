package config

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEnvOr(t *testing.T) {
	if got := envOr("ART_TEST_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("envOr fallback: got %q", got)
	}
	t.Setenv("ART_TEST_SET", "hi")
	if got := envOr("ART_TEST_SET", "fallback"); got != "hi" {
		t.Fatalf("envOr set: got %q", got)
	}
}

func TestDecodeKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(make([]byte, 32))
	b, err := decodeKey(good)
	if err != nil || len(b) != 32 {
		t.Fatalf("good key: %v len=%d", err, len(b))
	}
	if _, err := decodeKey(""); err != nil {
		t.Fatalf("empty key should return nil-nil: %v", err)
	}
	if _, err := decodeKey("not-base64-padded====="); err == nil {
		t.Fatal("expected error for invalid base64")
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := decodeKey(short); err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestOwnerAllowed(t *testing.T) {
	c := &Config{OwnerEmails: []string{"a@x.com", "b@y.com"}}
	if !c.OwnerAllowed("A@X.com") {
		t.Fatal("case insensitive lookup failed")
	}
	if !c.OwnerAllowed("  b@y.com  ") {
		t.Fatal("whitespace trim failed")
	}
	if c.OwnerAllowed("c@z.com") {
		t.Fatal("non-allowlisted email should be rejected")
	}
}

func TestLoadValidate(t *testing.T) {
	clearEnv := func() {
		for _, k := range []string{
			"DATABASE_URL", "OWNER_EMAILS", "GOOGLE_OAUTH_CLIENT_ID",
			"GOOGLE_OAUTH_CLIENT_SECRET", "VERTEX_PROJECT_ID",
			"TOKEN_ENCRYPTION_KEY", "OIDC_AUDIENCE",
		} {
			_ = os.Unsetenv(k)
		}
	}
	clearEnv()
	defer clearEnv()

	if _, err := Load(); err == nil {
		t.Fatal("expected missing-env error")
	} else if !strings.Contains(err.Error(), "missing required") {
		t.Fatalf("unexpected error: %v", err)
	}

	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	t.Setenv("DATABASE_URL", "postgres://localhost/x")
	t.Setenv("OWNER_EMAILS", "you@example.com")
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "cid")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "csec")
	t.Setenv("VERTEX_PROJECT_ID", "p")
	t.Setenv("TOKEN_ENCRYPTION_KEY", key)
	t.Setenv("OIDC_AUDIENCE", "aud")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.OwnerAllowed("you@example.com") {
		t.Fatal("owner email not picked up")
	}
	if cfg.Vertex.Location == "" {
		t.Fatal("Vertex location default missing")
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/x")
	t.Setenv("OWNER_EMAILS", "you@example.com")
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "cid")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "csec")
	t.Setenv("TOKEN_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("OIDC_AUDIENCE", "aud")
	t.Setenv("VERTEX_PROJECT_ID", "proj")
	_ = os.Unsetenv("VERTEX_MODEL")
	_ = os.Unsetenv("ART_PLANNER")
	_ = os.Unsetenv("ART_CRON_INTERVAL")
	_ = os.Unsetenv("ART_SYNC_PAST_DAYS")
	_ = os.Unsetenv("ART_SYNC_FUTURE_DAYS")
}

func TestLoadDefaults(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Planner != PlannerLLM {
		t.Fatalf("default planner: got %q, want %q", cfg.Planner, PlannerLLM)
	}
	if cfg.Vertex.Model != "gemini-3.1-pro" {
		t.Fatalf("default model: got %q", cfg.Vertex.Model)
	}
	if cfg.CronInterval != time.Hour {
		t.Fatalf("default cron interval: got %v", cfg.CronInterval)
	}
}

// GCP is a hard boot requirement, regardless of planner mode.
func TestLoadRequiresVertex(t *testing.T) {
	setRequiredEnv(t)
	_ = os.Unsetenv("VERTEX_PROJECT_ID")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "VERTEX_PROJECT_ID") {
		t.Fatalf("boot without VERTEX_PROJECT_ID should fail naming it, got: %v", err)
	}
	t.Setenv("ART_PLANNER", "deterministic")
	if _, err := Load(); err == nil {
		t.Fatal("deterministic mode still requires VERTEX_PROJECT_ID to boot")
	}
}

func TestLoadDeterministicPlannerOptIn(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ART_PLANNER", "deterministic")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Planner != PlannerDeterministic || cfg.LLMEnabled() {
		t.Fatalf("planner: got %q (llm=%v), want explicit deterministic", cfg.Planner, cfg.LLMEnabled())
	}
}

func TestLoadPlannerInvalid(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ART_PLANNER", "psychic")
	if _, err := Load(); err == nil {
		t.Fatal("invalid ART_PLANNER should fail")
	}
}

func TestLoadVertexModelOverride(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("VERTEX_MODEL", "gemini-4.0-flash")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vertex.Model != "gemini-4.0-flash" {
		t.Fatalf("model override: got %q", cfg.Vertex.Model)
	}
}

func TestLoadCronInterval(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ART_CRON_INTERVAL", "30m")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CronInterval != 30*time.Minute {
		t.Fatalf("cron interval: got %v", cfg.CronInterval)
	}
	t.Setenv("ART_CRON_INTERVAL", "sometimes")
	if _, err := Load(); err == nil {
		t.Fatal("invalid ART_CRON_INTERVAL should fail")
	}
}

func TestLoadSyncWindows(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SyncPastDays != 365 || cfg.SyncFutureDays != 60 {
		t.Fatalf("defaults: past=%d future=%d", cfg.SyncPastDays, cfg.SyncFutureDays)
	}

	t.Setenv("ART_SYNC_PAST_DAYS", "30")
	t.Setenv("ART_SYNC_FUTURE_DAYS", "21")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load with overrides: %v", err)
	}
	if cfg.SyncPastDays != 30 || cfg.SyncFutureDays != 21 {
		t.Fatalf("overrides: past=%d future=%d", cfg.SyncPastDays, cfg.SyncFutureDays)
	}

	// The planner books 14 days out; syncing less than that would leave the
	// planner blind to busy time it schedules around.
	t.Setenv("ART_SYNC_FUTURE_DAYS", "7")
	if _, err := Load(); err == nil {
		t.Fatal("future window below the planning horizon should fail")
	}
	t.Setenv("ART_SYNC_FUTURE_DAYS", "x")
	if _, err := Load(); err == nil {
		t.Fatal("non-numeric ART_SYNC_FUTURE_DAYS should fail")
	}
}

func TestLoadBadTimezone(t *testing.T) {
	t.Setenv("ART_TIMEZONE", "Not/A/Zone")
	t.Setenv("DATABASE_URL", "x")
	t.Setenv("OWNER_EMAILS", "a@b.com")
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "x")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "x")
	t.Setenv("VERTEX_PROJECT_ID", "x")
	t.Setenv("TOKEN_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("OIDC_AUDIENCE", "x")
	if _, err := Load(); err == nil {
		t.Fatal("expected timezone error")
	}
}
