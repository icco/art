package tui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	teatest "github.com/charmbracelet/x/exp/teatest/v2"
)

// testModel builds a root model backed by a stubbed client pointed at server.
func testModel(t *testing.T, server *httptest.Server) *teatest.TestModel {
	t.Helper()
	root := newRootWithClient(Config{APIURL: server.URL}, stubClient(server), false)
	return teatest.NewTestModel(t, root, teatest.WithInitialTermSize(100, 40))
}

// ansiRE strips escape sequences; the renderer's diffing can split literal
// text mid-string (e.g. "run\x1b[4h triage").
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\a]*\a`)

func waitForContains(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(ansiRE.ReplaceAll(b, nil), []byte(want))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(25*time.Millisecond))
}

// emptyAPI serves empty JSON lists for every GET so pages render without data.
func emptyAPI() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
}

func TestNavigationRendersEachPage(t *testing.T) {
	server := emptyAPI()
	defer server.Close()
	tm := testModel(t, server)

	// Dashboard is the home screen.
	waitForContains(t, tm, "LAST RUNS")

	tm.Type("2") // digest
	waitForContains(t, tm, "Press t to run triage")

	tm.Type("3") // settings
	waitForContains(t, tm, "Settings")

	tm.Type("q")
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
