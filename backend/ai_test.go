package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentUsesToolsAndValidatesReport(t *testing.T) {
	github := mockGitHub(t)
	defer github.Close()
	g := newGitHub("acme/widget", 12, "")
	g.BaseURL = github.URL
	turns := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer personal-key" {
			t.Error("wrong run-scoped key")
		}
		turns++
		name := "search_paths"
		args := map[string]any{"query": "cache"}
		if turns == 2 {
			name = "submit_report"
			args = map[string]any{"summary": "Cache behavior needs investigation.", "findings": []map[string]any{{"claim": "The issue reports a cache miss.", "confidence": "low", "evidence_ids": []string{"E1"}}}, "questions": []string{"Which version?"}, "limitations": []string{"Not reproduced"}, "draft": "Please provide reproduction steps."}
		}
		encoded, _ := json.Marshal(args)
		_ = json.NewEncoder(w).Encode(map[string]any{"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5}, "choices": []map[string]any{{"message": map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"id": "call", "function": map[string]any{"name": name, "arguments": string(encoded)}}}}}}})
	}))
	defer model.Close()
	report, mode, err := investigate(context.Background(), g, func(string, string) {}, "auto", "personal-key", "gpt-4.1-mini", model.URL)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "ai" || turns != 2 || report.Findings[0].EvidenceIDs[0] != "E1" {
		t.Fatalf("unexpected result: mode=%s turns=%d report=%+v", mode, turns, report)
	}
	if g.Metrics.ModelTurns != 2 || g.Metrics.ToolCalls != 2 {
		t.Fatalf("missing metrics: %+v", g.Metrics)
	}
}
