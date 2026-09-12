package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCIStartPersistsKindAndSharesLimits(t *testing.T) {
	s := testServer(t)
	s.OpenAIKey = "shared"
	s.launch = func(func()) {}
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/runs", strings.NewReader(body)))
		return w
	}
	w := post(`{"kind":"ci","url":"https://github.com/acme/widget/actions/runs/123/attempts/2","mode":"auto"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var run Run
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Kind != "ci" || run.OwnerID != "user-a" || run.Number != 123 || !strings.HasSuffix(run.URL, "/attempts/2") || run.Title != "CI run #123" {
		t.Fatalf("bad CI run %+v", run)
	}
	if w := post(`{"url":"https://github.com/acme/widget/issues/12","mode":"auto"}`); w.Code != 429 {
		t.Fatalf("CI bypassed shared allowance: %d", w.Code)
	}
	if w := post(`{"kind":"ci","url":"https://github.com/acme/widget/actions/runs/124","openai_api_key":"personal","github_token":"secret"}`); w.Code != 202 {
		t.Fatalf("personal key blocked: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"kind":"ci","url":"https://github.com/acme/widget/issues/12"}`, `{"kind":"other","url":"https://github.com/acme/widget/actions/runs/123"}`} {
		if w := post(body); w.Code != 422 {
			t.Fatalf("invalid input accepted: %d", w.Code)
		}
	}
	list := httptest.NewRecorder()
	s.handler().ServeHTTP(list, httptest.NewRequest("GET", "/api/runs", nil))
	if !strings.Contains(list.Body.String(), `"kind":"ci"`) || strings.Contains(list.Body.String(), "secret") {
		t.Fatalf("bad list %s", list.Body.String())
	}
	run.Status = "ready"
	run.Report = &Report{Summary: "CI failed", Draft: "check logs"}
	run.Draft = "check logs"
	if err := s.Store.Save(&run); err != nil {
		t.Fatal(err)
	}
	export := httptest.NewRecorder()
	s.handler().ServeHTTP(export, httptest.NewRequest("GET", "/api/runs/"+run.ID+"/export", nil))
	if export.Code != 200 || !strings.Contains(export.Header().Get("Content-Disposition"), "ci-123-report.md") {
		t.Fatal("wrong CI export")
	}
	foreign := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/runs/"+run.ID, nil)
	req.Header.Set("X-Test-User", "user-b")
	s.handler().ServeHTTP(foreign, req)
	if foreign.Code != 404 {
		t.Fatal("foreign CI report accessible")
	}
}
