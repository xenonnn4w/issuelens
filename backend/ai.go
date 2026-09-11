package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type toolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type chatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func function(name, description string, properties map[string]any) map[string]any {
	required := []string{}
	for key := range properties {
		required = append(required, key)
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description, "parameters": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}}
}

var reportSchema = map[string]any{"type": "object", "properties": map[string]any{"summary": map[string]any{"type": "string"}, "findings": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"claim": map[string]any{"type": "string"}, "evidence_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "confidence": map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}}}, "required": []string{"claim", "evidence_ids", "confidence"}}}, "questions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "limitations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "draft": map[string]any{"type": "string"}}, "required": []string{"summary", "findings", "questions", "limitations", "draft"}}

var tools = []map[string]any{
	function("search_code", "Search bounded candidate source file contents.", map[string]any{"query": map[string]any{"type": "string"}}),
	function("search_paths", "Find repository paths by keyword.", map[string]any{"query": map[string]any{"type": "string"}}),
	function("read_file", "Read up to 100 lines of a known file at the pinned commit.", map[string]any{"path": map[string]any{"type": "string"}, "start_line": map[string]any{"type": "integer"}}),
	function("search_issues", "Search previous issues in this repository.", map[string]any{"query": map[string]any{"type": "string"}}),
	{"type": "function", "function": map[string]any{"name": "submit_report", "description": "Submit the cited final report.", "parameters": reportSchema}},
}

func callModel(ctx context.Context, key, model, baseURL string, messages []chatMessage, available []map[string]any, choice any) (*chatResponse, error) {
	payload := map[string]any{"model": model, "messages": messages, "tools": available, "tool_choice": choice, "parallel_tool_calls": false, "max_completion_tokens": 2500}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("The model provider could not be reached.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Model provider returned HTTP %d. Check the API key and model configuration.", resp.StatusCode)
	}
	var result chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return nil, errors.New("Model provider returned an invalid response")
	}
	if len(result.Choices) == 0 {
		return nil, errors.New("Model provider returned no choices")
	}
	return &result, nil
}

func toolResult(ctx context.Context, g *GitHub, call toolCall) (any, error) {
	var args struct {
		Query     string `json:"query"`
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return nil, err
	}
	switch call.Function.Name {
	case "search_paths":
		return g.SearchPaths(args.Query), nil
	case "search_code":
		return g.SearchCode(ctx, args.Query)
	case "search_issues":
		return g.SearchIssues(ctx, args.Query)
	case "read_file":
		return g.ReadFile(ctx, args.Path, args.StartLine)
	default:
		return nil, errors.New("Unknown tool")
	}
}

func aiInvestigate(ctx context.Context, g *GitHub, emit emitFunc, key, model, baseURL string) (*Report, error) {
	issueData, _ := json.Marshal(map[string]any{"issue": g.Issue, "evidence": g.Evidence})
	messages := []chatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: string(issueData)}}
	seen := map[string]bool{}
	calls := 0
	noProgress := 0
	finishReason := ""
	for turn := 0; turn < 10; turn++ {
		if finishReason == "" && (turn >= 8 || calls >= 16) {
			finishReason = "Investigation budget reserved for final reporting."
		}
		choice := any("required")
		if finishReason != "" {
			emit("finalize", finishReason+" Preparing report from collected evidence")
			messages = append(messages, chatMessage{Role: "system", Content: finishReason + " Call submit_report now. State unresolved questions."})
			choice = map[string]any{"type": "function", "function": map[string]any{"name": "submit_report"}}
		}
		emit("reason", fmt.Sprintf("Investigation step %d/10", turn+1))
		response, err := callModel(ctx, key, model, baseURL, messages, tools, choice)
		if err != nil {
			return nil, err
		}
		g.Metrics.ModelTurns++
		g.Metrics.InputTokens += response.Usage.PromptTokens
		g.Metrics.OutputTokens += response.Usage.CompletionTokens
		message := response.Choices[0].Message
		messages = append(messages, message)
		for _, call := range message.ToolCalls {
			calls++
			g.Metrics.ToolCalls = calls
			if calls > 18 {
				return nil, errors.New("Agent tool budget exhausted. Retry in retrieval mode.")
			}
			if call.Function.Name == "submit_report" {
				var report Report
				if err := json.Unmarshal([]byte(call.Function.Arguments), &report); err == nil {
					limits := append([]string{}, g.Warnings...)
					limits = append(limits, "Code was inspected at the current default-branch snapshot. No reproduction tests were executed.")
					if finishReason != "" {
						limits = append(limits, finishReason)
					}
					if done, err := finishReport(&report, g.Evidence, limits); err == nil {
						return done, nil
					}
				}
				messages = append(messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: `{"error":"Every finding must cite observed evidence IDs; submit a valid report."}`})
				continue
			}
			signature := call.Function.Name + ":" + call.Function.Arguments
			if seen[signature] {
				finishReason = "Repeated tool request detected; investigation stopped to prevent a search loop."
				messages = append(messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: `{"error":"This request was already executed."}`})
				continue
			}
			seen[signature] = true
			emit("tool", call.Function.Name+": "+call.Function.Arguments)
			before := len(g.Evidence)
			result, err := toolResult(ctx, g, call)
			if len(g.Evidence) > before {
				noProgress = 0
			} else {
				noProgress++
				if noProgress >= 3 {
					finishReason = "Three tool requests added no new evidence."
				}
			}
			if err != nil {
				result = map[string]any{"error": err.Error()}
			}
			encoded, _ := json.Marshal(result)
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: string(encoded)})
		}
	}
	return nil, errors.New("Agent reached its turn limit without a valid report. Collected evidence is preserved.")
}

func recoverReport(ctx context.Context, run *Run, emit emitFunc, key, model, baseURL string) (*Report, Metrics, error) {
	user, _ := json.Marshal(map[string]any{"issue": run.Title, "evidence": run.Evidence, "previous_failure": run.PreviousFailure})
	messages := []chatMessage{{Role: "system", Content: systemPrompt + " Exploration has ended. Use only saved evidence and submit a report now."}, {Role: "user", Content: string(user)}}
	metrics := Metrics{}
	for attempt := 0; attempt < 2; attempt++ {
		emit("finalize", fmt.Sprintf("Generating report from saved evidence (%d/2); no GitHub requests", attempt+1))
		choice := map[string]any{"type": "function", "function": map[string]any{"name": "submit_report"}}
		response, err := callModel(ctx, key, model, baseURL, messages, tools[4:], choice)
		if err != nil {
			return nil, metrics, err
		}
		metrics.ModelTurns++
		metrics.InputTokens += response.Usage.PromptTokens
		metrics.OutputTokens += response.Usage.CompletionTokens
		message := response.Choices[0].Message
		messages = append(messages, message)
		for _, call := range message.ToolCalls {
			metrics.ToolCalls++
			var report Report
			if call.Function.Name == "submit_report" && json.Unmarshal([]byte(call.Function.Arguments), &report) == nil {
				if done, err := finishReport(&report, run.Evidence, []string{"Recovered from saved evidence after an interrupted investigation. Coverage is incomplete; no new GitHub requests were made."}); err == nil {
					return done, metrics, nil
				}
			}
			messages = append(messages, chatMessage{Role: "tool", ToolCallID: call.ID, Content: `{"error":"Submit a report with observed evidence IDs."}`})
		}
	}
	return nil, metrics, errors.New("Model could not produce a valid cited report from saved evidence.")
}
