package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseCI(t *testing.T) {
	for _, raw := range []string{"https://github.com/acme/widget/actions/runs/123", "github.com/acme/widget/actions/runs/123/job/456?check_suite=1", "https://github.com/acme/widget/actions/runs/123/attempts/2"} {
		repo, number, attempt, err := parseCI(raw)
		if err != nil || repo != "acme/widget" || number != 123 {
			t.Fatalf("%s: %s %d %v", raw, repo, number, err)
		}
		if strings.Contains(raw, "attempts") && attempt != 2 {
			t.Fatal("attempt discarded")
		}
	}
	for _, raw := range []string{"https://github.com.evil.test/a/b/actions/runs/1", "https://user@github.com/a/b/actions/runs/1", "https://github.com:443/a/b/actions/runs/1", "http://github.com/a/b/actions/runs/1", "https://github.com/a/b/issues/1", "https://github.com/a/b/actions/runs/0", "https://github.com/a/b/actions/runs/999999999999999999999", "https://github.com/a/b/actions/runs/1/attempts/0"} {
		if _, _, _, err := parseCI(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestCILiveSmoke(t *testing.T) {
	raw := os.Getenv("ISSUELENS_CI_SMOKE_URL")
	if raw == "" {
		t.Skip("Set ISSUELENS_CI_SMOKE_URL to test real GitHub collection")
	}
	repo, number, attempt, err := parseCI(raw)
	if err != nil {
		t.Fatal(err)
	}
	g := newGitHub(repo, number, os.Getenv("ISSUELENS_CI_SMOKE_TOKEN"))
	g.CI = true
	g.CIAttempt = attempt
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	report, mode, err := investigate(ctx, g, func(string, string) {}, "retrieval", "", "", "")
	if err != nil {
		t.Fatalf("%v; limitations: %v", err, g.Warnings)
	}
	if err := validateReport(report, g.Evidence); err != nil {
		t.Fatal(err)
	}
	if mode != "retrieval" || g.SHA == "" {
		t.Fatal("Missing pinned snapshot")
	}
	t.Logf("Collected %d evidence items in %d requests", len(g.Evidence), g.Requests)
}

func mockCI(t *testing.T, status, conclusion string, private bool, logStatus int) *httptest.Server {
	t.Helper()
	sha := strings.Repeat("b", 40)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value any
		switch r.URL.Path {
		case "/repos/acme/widget":
			value = map[string]any{"private": private}
		case "/repos/acme/widget/actions/runs/123/attempts/2":
			value = map[string]any{"id": 123, "name": "Build", "status": status, "conclusion": conclusion, "head_sha": sha, "run_attempt": 2, "path": ".github/workflows/ci.yml", "event": "push", "head_branch": "main"}
		case "/repos/acme/widget/actions/runs/123/attempts/2/jobs":
			value = map[string]any{"total_count": 2, "jobs": []any{
				map[string]any{"id": 456, "name": "Go tests", "conclusion": "failure", "steps": []any{map[string]any{"number": 3, "name": "go test", "conclusion": "failure"}}},
				map[string]any{"id": 457, "name": "Lint", "conclusion": "success"},
			}}
		case "/repos/acme/widget/actions/jobs/456/logs":
			w.WriteHeader(logStatus)
			_, _ = w.Write([]byte("2026-01-01 ##[error] FAIL TestCache src/cache.go:12\nAPI_KEY=sk-proj-123456789abcdefghijklmnop\ncustom-credential\nProcess completed with exit code 1"))
			return
		case "/repos/acme/widget/git/trees/" + sha:
			value = map[string]any{"tree": []any{
				map[string]any{"path": ".github/workflows/ci.yml", "type": "blob", "mode": "100644", "size": 40, "sha": "workflow"},
				map[string]any{"path": "src/cache.go", "type": "blob", "mode": "100644", "size": 40, "sha": "code"},
			}}
		case "/repos/acme/widget/git/blobs/workflow":
			value = map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("jobs:\n  tests:\n    steps:\n      - run: go test ./...\n"))}
		case "/repos/acme/widget/git/blobs/code":
			value = map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("func cache() {}\n"))}
		case "/repos/acme/widget/commits/" + sha:
			value = map[string]any{"files": []any{map[string]any{"filename": "src/cache.go", "status": "modified", "patch": "@@ -1 +1 @@\n-old\n+new"}}}
		default:
			t.Errorf("unexpected GitHub request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
}

func TestCICollectsFailedAttemptAndPinnedEvidence(t *testing.T) {
	server := mockCI(t, "completed", "failure", false, 200)
	defer server.Close()
	g := newGitHub("acme/widget", 123, "custom-credential")
	g.CI = true
	g.CIAttempt = 2
	g.BaseURL = server.URL
	report, mode, err := investigate(context.Background(), g, func(string, string) {}, "retrieval", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "retrieval" || len(report.Findings) != 1 || g.SHA != strings.Repeat("b", 40) {
		t.Fatalf("bad result %+v", report)
	}
	kinds := map[string]bool{}
	for _, e := range g.Evidence {
		kinds[e.Kind] = true
		if strings.Contains(e.Text, "sk-proj-") || strings.Contains(e.Text, "custom-credential") {
			t.Fatal("credential persisted")
		}
		if e.Kind == "code" && !strings.Contains(e.URL, g.SHA) {
			t.Fatal("workflow not pinned")
		}
		if e.Kind == "log" && !strings.Contains(e.Text, "FAIL TestCache") {
			t.Fatal("failure omitted")
		}
	}
	for _, kind := range []string{"workflow", "job", "log", "code", "diff"} {
		if !kinds[kind] {
			t.Errorf("missing %s evidence", kind)
		}
	}
	if !strings.Contains(report.Draft, "review") {
		t.Fatal("retrieval implies diagnosis")
	}
}

func TestCIRejectsUnsupportedRunsAndUnavailableLogs(t *testing.T) {
	for _, tc := range []struct {
		name, status, conclusion string
		private                  bool
		logStatus                int
	}{
		{"private", "completed", "failure", true, 200}, {"running", "in_progress", "", false, 200}, {"successful", "completed", "success", false, 200}, {"expired logs", "completed", "failure", false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := mockCI(t, tc.status, tc.conclusion, tc.private, tc.logStatus)
			defer server.Close()
			g := newGitHub("acme/widget", 123, "")
			g.CI = true
			g.BaseURL = server.URL
			if err := g.InitializeCI(context.Background(), 2, func(string, string) {}); err == nil {
				t.Fatal("accepted unsupported run")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLogRedirectDoesNotTransmitGitHubCredentials(t *testing.T) {
	g := newGitHub("acme/widget", 123, "private-token")
	calls := 0
	g.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			if r.Header.Get("Authorization") != "Bearer private-token" {
				t.Fatal("missing API auth")
			}
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://storage.blob.core.windows.net/logs?signed=secret"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatal("credentials forwarded to storage")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("##[error] failed")), Request: r}, nil
	})}
	text, err := g.jobLog(context.Background(), 456)
	if err != nil || !strings.Contains(text, "failed") || calls != 2 {
		t.Fatalf("%q %v %d", text, err, calls)
	}
	for _, raw := range []string{"http://storage.blob.core.windows.net/log", "https://evil.test/log", "https://storage.blob.core.windows.net.evil.test/log", "https://user@storage.blob.core.windows.net/log", "https://storage.blob.core.windows.net:8443/log"} {
		u, _ := http.NewRequest("GET", raw, nil)
		if allowedLogURL(u.URL) {
			t.Errorf("unsafe destination %s", raw)
		}
	}
}

func TestLogExcerptsAreBoundedAndKeepFailures(t *testing.T) {
	text := strings.Repeat("ordinary output\n", 1000) + "##[error] compiler failed\n" + strings.Repeat("tail\n", 1000)
	excerpt := logExcerpt(text)
	if !strings.Contains(excerpt, "compiler failed") || !strings.Contains(excerpt, "2002:") || len(excerpt) > 12000 {
		t.Fatalf("bad excerpt %d", len(excerpt))
	}
}

func TestCIModelReceivesCIContextAndProducesCitedReport(t *testing.T) {
	github := mockCI(t, "completed", "failure", false, 200)
	defer github.Close()
	g := newGitHub("acme/widget", 123, "custom-credential")
	g.CI = true
	g.CIAttempt = 2
	g.BaseURL = github.URL
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []chatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(body)
		if !strings.Contains(string(encoded), "failed GitHub Actions") || strings.Contains(string(encoded), "custom-credential") {
			t.Fatal("wrong or unredacted context")
		}
		args, _ := json.Marshal(Report{Summary: "Go tests failed.", Findings: []Finding{{Claim: "The Go test job failed at step 3.", EvidenceIDs: []string{"E2"}, Confidence: "high"}}, Questions: []string{}, Limitations: []string{}, Draft: "Inspect TestCache and rerun go test ./... after a targeted change. This fix is unverified."})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "report", "function": map[string]any{"name": "submit_report", "arguments": string(args)}}}}}}})
	}))
	defer model.Close()
	report, mode, err := investigate(context.Background(), g, func(string, string) {}, "auto", "model-key", "model", model.URL)
	if err != nil || mode != "ai" {
		t.Fatalf("%s %v", mode, err)
	}
	if !strings.Contains(strings.Join(report.Limitations, " "), "failed run's head commit") || strings.Contains(strings.Join(report.Limitations, " "), "default-branch") {
		t.Fatal("wrong snapshot limitations")
	}
}
