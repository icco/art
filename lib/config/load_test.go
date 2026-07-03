package config

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
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
	if cfg.Triage.BackfillDays != 7 || cfg.Triage.ReconcileDays != 7 {
		t.Errorf("triage window defaults = %d/%d, want 7/7", cfg.Triage.BackfillDays, cfg.Triage.ReconcileDays)
	}
	if cfg.Triage.MaxPerRun != 1000 {
		t.Errorf("MaxPerRun default = %d, want 1000", cfg.Triage.MaxPerRun)
	}
}

func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "x")
	t.Setenv("OWNER_EMAILS", "a@b.com")
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "x")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "x")
	t.Setenv("VERTEX_PROJECT_ID", "x")
	t.Setenv("TOKEN_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("OIDC_AUDIENCE", "x")
}

// A set-but-unparseable env var must fail Load, not silently fall back:
// TRIAGE_DRY_RUN=yes previously became false and ran triage live.
func TestLoadRejectsUnparseableEnv(t *testing.T) {
	setValidEnv(t)
	t.Setenv("TRIAGE_DRY_RUN", "yes")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRIAGE_DRY_RUN") {
		t.Fatalf("want TRIAGE_DRY_RUN parse error, got %v", err)
	}
}

func TestLoadRejectsUnparseableInt(t *testing.T) {
	setValidEnv(t)
	t.Setenv("TRIAGE_MAX_PER_RUN", "1,000")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRIAGE_MAX_PER_RUN") {
		t.Fatalf("want TRIAGE_MAX_PER_RUN parse error, got %v", err)
	}
}

func TestLoadRejectsOutOfRangeThreshold(t *testing.T) {
	setValidEnv(t)
	t.Setenv("TRIAGE_CONFIDENCE_THRESHOLD", "1.5")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRIAGE_CONFIDENCE_THRESHOLD") {
		t.Fatalf("want TRIAGE_CONFIDENCE_THRESHOLD range error, got %v", err)
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
