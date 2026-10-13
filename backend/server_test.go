package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	store, err := openStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s := newServer(store, "../web/out")
	s.authOverride = func(r *http.Request) string {
		if id := r.Header.Get("X-Test-User"); id != "" {
			return id
		}
		return "user-a"
	}
	return s
}

func TestHomeAndAPI(t *testing.T) {
	s := testServer(t)
	h := s.handler()
	entries, err := filepath.Glob("../web/out/static/build/app-*.js")
	if err != nil || len(entries) == 0 {
		t.Fatal("Missing built application entry")
	}
	entry := "/static/build/" + filepath.Base(entries[len(entries)-1])
	for _, p := range []string{"/", "/privacy", "/favicon.svg", entry, "/static/new.css", "/static/notice.css", "/static/mobile.css", "/api/config", "/api/usage", "/api/runs", "/healthz"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	r := newRun("test", "acme/widget", 12, "retrieval")
	r.OwnerID = "user-a"
	r.Status = "ready"
	r.Report = &Report{Summary: "summary", Draft: "draft"}
	r.Draft = "draft"
	if err := s.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/runs/test", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/usage", nil))
	var usage map[string]int
	_ = json.Unmarshal(w.Body.Bytes(), &usage)
	if usage["ready"] != 1 {
		t.Fatalf("usage: %v", usage)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PATCH", "/api/runs/test/review", strings.NewReader(`{"draft":"approved","approved":true}`)))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/runs/test/export", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "approved") {
		t.Fatalf("export: %s", w.Body.String())
	}
}

func TestRunEndpointsArePrivateToOwner(t *testing.T) {
	s := testServer(t)
	s.authOverride = func(r *http.Request) string { return r.Header.Get("X-Test-User") }
	s.launch = func(func()) {}
	h := s.handler()
	request := func(method, path, user, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			r.Header.Set("X-Test-User", user)
		}
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/runs", "", ""); w.Code != 401 {
		t.Fatalf("unsigned list: %d", w.Code)
	}
	if w := request("GET", "/api/usage", "", ""); w.Code != 401 {
		t.Fatalf("unsigned usage: %d", w.Code)
	}
	w := request("POST", "/api/runs", "user-a", `{"url":"https://github.com/acme/widget/issues/12","mode":"retrieval"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var run Run
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.OwnerID != "user-a" {
		t.Fatalf("owner not saved: %q", run.OwnerID)
	}
	for _, path := range []string{"/api/runs/" + run.ID, "/api/runs/" + run.ID + "/export"} {
		if w := request("GET", path, "user-b", ""); w.Code != 404 {
			t.Fatalf("foreign %s: %d", path, w.Code)
		}
	}
	if w := request("PATCH", "/api/runs/"+run.ID+"/review", "user-b", `{"draft":"changed","approved":true}`); w.Code != 404 {
		t.Fatalf("foreign review: %d", w.Code)
	}
	if w := request("POST", "/api/runs/"+run.ID+"/recover", "user-b", `{}`); w.Code != 404 {
		t.Fatalf("foreign recovery: %d", w.Code)
	}
	if w := request("GET", "/api/runs", "user-b", ""); w.Code != 200 || strings.Contains(w.Body.String(), run.ID) {
		t.Fatalf("foreign list: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/usage", "user-b", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("foreign usage: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/runs/"+run.ID, "user-a", ""); w.Code != 200 {
		t.Fatalf("owner detail: %d", w.Code)
	}
}

func TestDeployedGatewayAndOrigin(t *testing.T) {
	s := testServer(t)
	s.Deployed = true
	s.Gateway = "gateway"
	s.AllowedHost = "issuelens.me"
	h := s.handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://issuelens.me/api/runs", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://issuelens.me/api/runs", nil)
	req.Header.Set("X-Workspace-Gateway", "gateway")
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "http://issuelens.me/api/runs", strings.NewReader(`{}`))
	req.Header.Set("X-Workspace-Gateway", "gateway")
	req.Header.Set("Origin", "https://evil.test")
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestRunScopedKeysStayOutOfStorage(t *testing.T) {
	s := testServer(t)
	s.Deployed = true
	s.DailyLimit = 10
	s.launch = func(func()) {}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/runs", strings.NewReader(`{"url":"https://github.com/acme/widget/issues/12","mode":"auto","openai_api_key":"personal-secret","github_token":"gh-secret"}`))
	s.start(w, req)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "personal-secret") || strings.Contains(w.Body.String(), "gh-secret") {
		t.Fatal("key leaked in response")
	}
	var run Run
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	saved, err := s.Store.Get(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(saved)
	if strings.Contains(string(data), "personal-secret") || strings.Contains(string(data), "gh-secret") {
		t.Fatal("key persisted")
	}
}

func TestDailyLimit(t *testing.T) {
	s := testServer(t)
	s.Deployed = true
	s.DailyLimit = 1
	r := newRun("first", "acme/widget", 1, "retrieval")
	_ = s.Store.Save(r)
	if s.dailyOK() {
		t.Fatal("daily limit not enforced")
	}
}

func TestSharedKeyLimitAndPersonalBypass(t *testing.T) {
	s := testServer(t)
	s.Deployed = true
	s.OpenAIKey = "shared-secret"
	s.DailyLimit = 1
	s.launch = func(func()) {}
	if err := s.Store.Save(newRun("retrieval", "acme/widget", 1, "retrieval")); err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.start(w, httptest.NewRequest("POST", "/api/runs", strings.NewReader(body)))
		return w
	}
	shared := `{"url":"https://github.com/acme/widget/issues/12","mode":"auto"}`
	if w := post(shared); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	config := httptest.NewRecorder()
	s.config(config, httptest.NewRequest("GET", "/api/config", nil))
	if !strings.Contains(config.Body.String(), `"shared_key_next_at":"20`) {
		t.Fatalf("shared key availability missing: %s", config.Body.String())
	}
	if w := post(shared); w.Code != 429 || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), `"code":"shared_key_rate_limited"`) || !strings.Contains(w.Body.String(), `"retry_at":"20`) {
		t.Fatalf("shared key limit response missing status or guidance: %d %s", w.Code, w.Body.String())
	}
	personal := `{"url":"https://github.com/acme/widget/issues/13","mode":"auto","openai_api_key":"personal-secret"}`
	if w := post(personal); w.Code != 202 {
		t.Fatalf("personal key should bypass shared and daily limits: %s", w.Body.String())
	}
	runs, err := s.Store.List(10)
	if err != nil || len(runs) != 3 {
		t.Fatalf("unexpected saved runs: %d %v", len(runs), err)
	}
}

func TestRecoveryUsesSameSharedKeyLimit(t *testing.T) {
	s := testServer(t)
	s.OpenAIKey = "shared-secret"
	s.launch = func(func()) {}
	original := newRun("failed", "acme/widget", 12, "ai")
	original.Status = "failed"
	original.Evidence = []Evidence{{ID: "E1", Kind: "issue", Title: "Report", URL: original.URL, Text: "Failure"}}
	if err := s.Store.Save(original); err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/runs/failed/recover", strings.NewReader(body))
		req.SetPathValue("id", "failed")
		s.recover(w, req)
		return w
	}
	if w := post(`{}`); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if w := post(`{}`); w.Code != 429 {
		t.Fatalf("recovery bypassed shared gate: %d %s", w.Code, w.Body.String())
	}
	if w := post(`{"openai_api_key":"personal-secret"}`); w.Code != 202 {
		t.Fatalf("personal recovery blocked: %d %s", w.Code, w.Body.String())
	}
}
