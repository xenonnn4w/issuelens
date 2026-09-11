package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseIssue(t *testing.T) {
	repo, n, err := parseIssue("https://github.com/acme/widget/issues/12/?utm=1")
	if err != nil || repo != "acme/widget" || n != 12 {
		t.Fatalf("unexpected parse: %q %d %v", repo, n, err)
	}
	for _, raw := range []string{"https://github.com.evil.test/acme/widget/issues/12", "https://github.com@evil.test/a/b/issues/1", "https://github.com/a/b/pull/1", "https://github.com:443/a/b/issues/1", "https://user@github.com/a/b/issues/1"} {
		if _, _, err := parseIssue(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func mockGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value any
		switch r.URL.Path {
		case "/repos/acme/widget":
			value = map[string]any{"private": false, "default_branch": "main"}
		case "/repos/acme/widget/issues/12":
			value = map[string]any{"title": "Cache misses after refresh", "body": "Refresh loses cache entry.", "comments": 0}
		case "/repos/acme/widget/issues/12/comments":
			value = []any{}
		case "/repos/acme/widget/commits/main":
			value = map[string]any{"sha": strings.Repeat("a", 40)}
		case "/repos/acme/widget/git/trees/" + strings.Repeat("a", 40):
			value = map[string]any{"tree": []map[string]any{{"type": "blob", "mode": "100644", "path": "src/cache.go", "sha": "blob1", "size": 40}}}
		case "/repos/acme/widget/git/blobs/blob1":
			value = map[string]any{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("func refresh() {\n cache.clear()\n}\n"))}
		case "/search/issues":
			value = map[string]any{"items": []map[string]any{{"number": 7, "title": "Older cache bug", "state": "closed", "body": "Earlier behavior"}}}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
}

func TestRetrievalPinsEvidence(t *testing.T) {
	server := mockGitHub(t)
	defer server.Close()
	g := newGitHub("acme/widget", 12, "")
	g.BaseURL = server.URL
	report, mode, err := investigate(context.Background(), g, func(string, string) {}, "retrieval", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if mode != "retrieval" || len(report.Findings) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(g.Evidence) < 3 {
		t.Fatalf("missing related/code evidence: %+v", g.Evidence)
	}
	var pinned bool
	for _, e := range g.Evidence {
		if strings.Contains(e.URL, "/blob/"+strings.Repeat("a", 40)) {
			pinned = true
		}
	}
	if !pinned {
		t.Fatal("no commit-pinned code citation")
	}
	if _, err := g.ReadFile(context.Background(), "../../etc/passwd", 1); err == nil {
		t.Fatal("read outside repository")
	}
}

func TestReportRejectsInventedCitation(t *testing.T) {
	r := &Report{Summary: "summary", Draft: "draft", Findings: []Finding{{Claim: "claim", Confidence: "high", EvidenceIDs: []string{"E999"}}}}
	if err := validateReport(r, []Evidence{{ID: "E1"}}); err == nil {
		t.Fatal("invented citation accepted")
	}
}
