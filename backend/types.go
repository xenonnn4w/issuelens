package main

import "time"

type Evidence struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}

type Event struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

type Finding struct {
	Claim       string   `json:"claim"`
	EvidenceIDs []string `json:"evidence_ids"`
	Confidence  string   `json:"confidence"`
}

type Report struct {
	Summary     string    `json:"summary"`
	Findings    []Finding `json:"findings"`
	Questions   []string  `json:"questions"`
	Limitations []string  `json:"limitations"`
	Draft       string    `json:"draft"`
}

type Metrics struct {
	GitHubRequests int `json:"github_requests"`
	ToolCalls      int `json:"tool_calls"`
	ModelTurns     int `json:"model_turns"`
	InputTokens    int `json:"input_tokens"`
	OutputTokens   int `json:"output_tokens"`
}

type Run struct {
	ID              string     `json:"id"`
	OwnerID         string     `json:"owner_id"`
	Created         string     `json:"created"`
	Repo            string     `json:"repo"`
	Number          int        `json:"number"`
	URL             string     `json:"url"`
	Title           string     `json:"title"`
	Status          string     `json:"status"`
	Mode            string     `json:"mode"`
	Events          []Event    `json:"events"`
	Evidence        []Evidence `json:"evidence"`
	Report          *Report    `json:"report"`
	Draft           string     `json:"draft,omitempty"`
	Approved        bool       `json:"approved"`
	Commit          string     `json:"commit,omitempty"`
	Error           string     `json:"error,omitempty"`
	Metrics         Metrics    `json:"metrics"`
	RecoveredFrom   string     `json:"recovered_from,omitempty"`
	PreviousFailure string     `json:"previous_failure,omitempty"`
	ReviewedAt      string     `json:"reviewed_at,omitempty"`
}

func newRun(id, repo string, number int, mode string) *Run {
	return &Run{ID: id, Created: time.Now().UTC().Format(time.RFC3339Nano), Repo: repo, Number: number, URL: issueURL(repo, number), Title: issueTitle(number), Status: "queued", Mode: mode, Events: []Event{}, Evidence: []Evidence{}}
}
