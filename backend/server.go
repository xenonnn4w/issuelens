package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Store         *Store
	WebRoot       string
	Deployed      bool
	Gateway       string
	AllowedHost   string
	OpenAIKey     string
	OpenAIModel   string
	OpenAIBaseURL string
	GitHubToken   string
	DailyLimit    int
	reviewUser    string
	mu            sync.Mutex
	queued        int
	slots         chan struct{}
	launch        func(func())
}

func newServer(store *Store, webRoot string) *Server {
	limit, _ := strconv.Atoi(env("PUBLIC_DAILY_RUN_LIMIT", "10"))
	if limit < 1 {
		limit = 10
	}
	return &Server{Store: store, WebRoot: webRoot, Deployed: os.Getenv("DEPLOYED") == "1", Gateway: os.Getenv("GATEWAY_TOKEN"), AllowedHost: os.Getenv("ALLOWED_HOSTS"), OpenAIKey: os.Getenv("OPENAI_API_KEY"), OpenAIModel: env("OPENAI_MODEL", "gpt-4.1-mini"), OpenAIBaseURL: env("OPENAI_BASE_URL", "https://api.openai.com/v1"), GitHubToken: os.Getenv("GITHUB_TOKEN"), DailyLimit: limit, reviewUser: env("WORKSPACE_USER", "maintainer"), slots: make(chan struct{}, 2)}
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/config", s.config)
	mux.HandleFunc("GET /api/usage", s.usage)
	mux.HandleFunc("GET /api/runs", s.list)
	mux.HandleFunc("POST /api/runs", s.start)
	mux.HandleFunc("GET /api/runs/{id}", s.detail)
	mux.HandleFunc("POST /api/runs/{id}/recover", s.recover)
	mux.HandleFunc("PATCH /api/runs/{id}/review", s.review)
	mux.HandleFunc("GET /api/runs/{id}/export", s.export)
	mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, filepath.Join(s.WebRoot, "favicon.svg"))
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(s.WebRoot, "static")))))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, filepath.Join(s.WebRoot, "index.html"))
	})
	return s.guard(mux)
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.AllowedHost != "" && r.Host != s.AllowedHost && !strings.HasPrefix(r.Host, "127.0.0.1:") && !strings.HasPrefix(r.Host, "localhost:") {
			http.Error(w, "Invalid host", 400)
			return
		}
		if s.Deployed && r.URL.Path != "/healthz" && (s.Gateway == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Workspace-Gateway")), []byte(s.Gateway)) != 1) {
			http.Error(w, "Authenticated gateway required", 401)
			return
		}
		if r.Method == "POST" || r.Method == "PATCH" || r.Method == "PUT" || r.Method == "DELETE" {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host && origin != "http://"+r.Host {
				http.Error(w, "Cross-origin writes are disabled", 403)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"detail": message})
}
func decodeBody(r *http.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4096))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.db.PingContext(r.Context()); err != nil {
		apiError(w, 503, "Database unavailable")
		return
	}
	jsonResponse(w, 200, map[string]string{"status": "ok"})
}
func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	deployment := "local"
	if s.Deployed {
		deployment = "server"
	}
	next, err := s.Store.SharedKeyNextAt()
	if err != nil {
		apiError(w, 500, "Could not read shared key availability")
		return
	}
	nextAt := ""
	if next.After(time.Now()) {
		nextAt = next.Format(time.RFC3339)
	}
	jsonResponse(w, 200, map[string]any{"ai": s.OpenAIKey != "", "github": s.GitHubToken != "", "model": s.OpenAIModel, "deployment": deployment, "review_user": s.reviewUser, "shared_key_next_at": nextAt, "limits": map[string]int{"active": 2, "queued": 6, "daily_public_runs": s.DailyLimit, "shared_key_interval_seconds": 3600, "github_requests": 36, "model_turns": 10, "tool_calls": 18, "timeout_seconds": 360}})
}
func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.List(1000000)
	if err != nil {
		apiError(w, 500, "Could not read usage")
		return
	}
	stats := map[string]int{"total": len(runs), "ready": 0, "failed": 0, "active": 0, "approved": 0, "ai": 0, "retrieval": 0, "tool_calls": 0, "github_requests": 0, "input_tokens": 0, "output_tokens": 0}
	for _, run := range runs {
		switch run.Status {
		case "ready":
			stats["ready"]++
		case "failed":
			stats["failed"]++
		case "queued", "running":
			stats["active"]++
		}
		if run.Approved {
			stats["approved"]++
		}
		if run.Mode == "ai" {
			stats["ai"]++
		} else {
			stats["retrieval"]++
		}
		stats["tool_calls"] += run.Metrics.ToolCalls
		stats["github_requests"] += run.Metrics.GitHubRequests
		stats["input_tokens"] += run.Metrics.InputTokens
		stats["output_tokens"] += run.Metrics.OutputTokens
	}
	jsonResponse(w, 200, stats)
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Store.List(100)
	if err != nil {
		apiError(w, 500, "Could not read investigations")
		return
	}
	items := []map[string]any{}
	for _, run := range runs {
		items = append(items, map[string]any{"id": run.ID, "created": run.Created, "url": run.URL, "title": run.Title, "repo": run.Repo, "status": run.Status, "mode": run.Mode, "approved": run.Approved})
	}
	jsonResponse(w, 200, items)
}
func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	run, err := s.Store.Get(r.PathValue("id"))
	if err == sql.ErrNoRows {
		apiError(w, 404, "Investigation not found")
		return
	}
	if err != nil {
		apiError(w, 500, "Could not read investigation")
		return
	}
	jsonResponse(w, 200, run)
}
func (s *Server) reserve() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queued >= 6 {
		return false
	}
	s.queued++
	return true
}
func (s *Server) release() { s.mu.Lock(); s.queued--; s.mu.Unlock() }
func (s *Server) dailyOK() bool {
	if !s.Deployed {
		return true
	}
	count, err := s.Store.CountToday()
	return err == nil && count < s.DailyLimit
}

func (s *Server) createQueuedRun(w http.ResponseWriter, run *Run, sharedKey bool) bool {
	allowed, next, err := s.Store.Create(run, sharedKey, time.Now().UTC())
	if err != nil {
		s.release()
		apiError(w, 500, "Could not save investigation")
		return false
	}
	if !allowed {
		s.release()
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(next).Seconds())+1)))
		jsonResponse(w, 429, map[string]string{
			"code":     "shared_key_rate_limited",
			"detail":   "The shared AI key allows one investigation per hour. Add your own OpenAI key to continue now.",
			"retry_at": next.Format(time.RFC3339),
		})
		return false
	}
	return true
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL         string `json:"url"`
		Mode        string `json:"mode"`
		OpenAIKey   string `json:"openai_api_key"`
		GitHubToken string `json:"github_token"`
	}
	if err := decodeBody(r, &input); err != nil {
		apiError(w, 422, "Invalid investigation request")
		return
	}
	repo, number, err := parseIssue(input.URL)
	if err != nil {
		apiError(w, 422, err.Error())
		return
	}
	if input.Mode == "" {
		input.Mode = "auto"
	}
	if input.Mode != "auto" && input.Mode != "retrieval" {
		apiError(w, 422, "Invalid mode")
		return
	}
	mode := "retrieval"
	if input.Mode == "auto" && (input.OpenAIKey != "" || s.OpenAIKey != "") {
		mode = "ai"
	}
	if mode == "retrieval" && input.OpenAIKey == "" && !s.dailyOK() {
		apiError(w, 429, "Today's public retrieval limit has been reached. Please try again tomorrow.")
		return
	}
	if !s.reserve() {
		apiError(w, 429, "Six investigations are already queued. Wait for one to finish.")
		return
	}
	run := newRun(newID(), repo, number, mode)
	if !s.createQueuedRun(w, run, mode == "ai" && input.OpenAIKey == "") {
		return
	}
	jsonResponse(w, 202, run)
	worker := func() { s.runJob(run.ID, input.Mode, input.OpenAIKey, input.GitHubToken) }
	if s.launch != nil {
		s.launch(worker)
	} else {
		go worker()
	}
}

func (s *Server) runJob(id, mode, openaiKey, githubToken string) {
	defer s.release()
	s.slots <- struct{}{}
	defer func() { <-s.slots }()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	run, err := s.Store.Get(id)
	if err != nil {
		return
	}
	run.Status = "running"
	_ = s.Store.Save(run)
	if githubToken == "" {
		githubToken = s.GitHubToken
	}
	g := newGitHub(run.Repo, run.Number, githubToken)
	emit := func(kind, message string) {
		run.Events = append(run.Events, Event{Kind: kind, Message: message, Time: time.Now().UTC().Format(time.RFC3339Nano)})
		run.Evidence = append([]Evidence{}, g.Evidence...)
		if title, ok := g.Issue["title"].(string); ok {
			run.Title = title
		}
		_ = s.Store.Save(run)
	}
	personalKey := openaiKey != ""
	if !personalKey {
		openaiKey = s.OpenAIKey
	}
	baseURL := s.OpenAIBaseURL
	if personalKey {
		baseURL = "https://api.openai.com/v1"
	}
	report, actualMode, err := investigate(ctx, g, emit, mode, openaiKey, s.OpenAIModel, baseURL)
	if err != nil {
		run.Status = "failed"
		run.Error = err.Error()
		if ctx.Err() != nil {
			run.Error = "Investigation timed out after six minutes."
		}
	} else {
		run.Status = "ready"
		run.Report = report
		run.Draft = report.Draft
		run.Mode = actualMode
		emit("complete", "Report ready for maintainer review")
	}
	run.Evidence = append([]Evidence{}, g.Evidence...)
	run.Commit = g.SHA
	g.Metrics.GitHubRequests = g.Requests
	run.Metrics = g.Metrics
	if err := s.Store.Save(run); err != nil {
		log.Printf("save investigation: %v", err)
	}
}

func (s *Server) recover(w http.ResponseWriter, r *http.Request) {
	original, err := s.Store.Get(r.PathValue("id"))
	if err == sql.ErrNoRows {
		apiError(w, 404, "Investigation not found")
		return
	}
	if err != nil {
		apiError(w, 500, "Could not read investigation")
		return
	}
	if original.Status != "failed" || len(original.Evidence) == 0 {
		apiError(w, 409, "Recovery requires a failed investigation with saved evidence")
		return
	}
	var input struct {
		OpenAIKey string `json:"openai_api_key"`
	}
	if r.ContentLength > 0 {
		if err := decodeBody(r, &input); err != nil {
			apiError(w, 422, "Invalid recovery request")
			return
		}
	}
	if input.OpenAIKey == "" && s.OpenAIKey == "" {
		apiError(w, 409, "Configure an OpenAI API key to generate a report")
		return
	}
	if !s.reserve() {
		apiError(w, 429, "The investigation queue is full")
		return
	}
	copy := *original
	copy.ID = newID()
	copy.Created = time.Now().UTC().Format(time.RFC3339Nano)
	copy.Status = "queued"
	copy.Mode = "ai"
	copy.Approved = false
	copy.Report = nil
	copy.Draft = ""
	copy.Events = []Event{}
	copy.Metrics = Metrics{}
	copy.RecoveredFrom = original.ID
	copy.PreviousFailure = original.Error
	copy.Error = ""
	if !s.createQueuedRun(w, &copy, input.OpenAIKey == "") {
		return
	}
	jsonResponse(w, 202, &copy)
	worker := func() { s.runRecovery(copy.ID, input.OpenAIKey) }
	if s.launch != nil {
		s.launch(worker)
	} else {
		go worker()
	}
}

func (s *Server) runRecovery(id, key string) {
	defer s.release()
	s.slots <- struct{}{}
	defer func() { <-s.slots }()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	run, err := s.Store.Get(id)
	if err != nil {
		return
	}
	run.Status = "running"
	_ = s.Store.Save(run)
	emit := func(kind, message string) {
		run.Events = append(run.Events, Event{Kind: kind, Message: message, Time: time.Now().UTC().Format(time.RFC3339Nano)})
		_ = s.Store.Save(run)
	}
	personalKey := key != ""
	if !personalKey {
		key = s.OpenAIKey
	}
	baseURL := s.OpenAIBaseURL
	if personalKey {
		baseURL = "https://api.openai.com/v1"
	}
	report, metrics, err := recoverReport(ctx, run, emit, key, s.OpenAIModel, baseURL)
	run.Metrics = metrics
	if err != nil {
		run.Status = "failed"
		run.Error = err.Error()
	} else {
		run.Status = "ready"
		run.Report = report
		run.Draft = report.Draft
		emit("complete", "Report recovered from saved evidence; ready for review")
	}
	_ = s.Store.Save(run)
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	run, err := s.Store.Get(r.PathValue("id"))
	if err == sql.ErrNoRows {
		apiError(w, 404, "Investigation not found")
		return
	}
	if err != nil {
		apiError(w, 500, "Could not read investigation")
		return
	}
	if run.Status != "ready" {
		apiError(w, 409, "Only completed reports can be reviewed")
		return
	}
	var input struct {
		Draft    string `json:"draft"`
		Approved bool   `json:"approved"`
	}
	if err := decodeBody(r, &input); err != nil || strings.TrimSpace(input.Draft) == "" || len(input.Draft) > 20000 {
		apiError(w, 422, "A response draft is required")
		return
	}
	run.Draft = input.Draft
	run.Approved = input.Approved
	run.ReviewedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.Store.Save(run); err != nil {
		apiError(w, 500, "Could not save review")
		return
	}
	jsonResponse(w, 200, run)
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	run, err := s.Store.Get(r.PathValue("id"))
	if err == sql.ErrNoRows {
		apiError(w, 404, "Investigation not found")
		return
	}
	if err != nil {
		apiError(w, 500, "Could not read investigation")
		return
	}
	if run.Report == nil {
		apiError(w, 409, "Report is not ready")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=issue-%d-report.md", run.Number))
	_, _ = w.Write([]byte(markdown(run)))
}

func markdown(run *Run) string {
	r := run.Report
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n\nMode: %s\nCommit: %s\n\n## Summary\n%s\n\n## Findings\n", run.Title, run.URL, run.Mode, run.Commit, r.Summary)
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "\n- [%s] %s (%s)\n", f.Confidence, f.Claim, strings.Join(f.EvidenceIDs, ", "))
	}
	b.WriteString("\n## Open questions\n")
	for _, q := range r.Questions {
		b.WriteString("- " + q + "\n")
	}
	b.WriteString("\n## Limitations\n")
	for _, limit := range r.Limitations {
		b.WriteString("- " + limit + "\n")
	}
	b.WriteString("\n## Sources\n")
	for _, e := range run.Evidence {
		fmt.Fprintf(&b, "- %s: [%s](%s)\n", e.ID, e.Title, e.URL)
	}
	state := "unapproved"
	if run.Approved {
		state = "approved"
	}
	fmt.Fprintf(&b, "\n## Response draft (%s)\n\n%s\n", state, run.Draft)
	return b.String()
}
