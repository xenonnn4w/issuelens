package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var issuePath = regexp.MustCompile(`^/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/([1-9][0-9]*)/?$`)
var wordPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_]{2,}`)

func parseIssue(raw string) (string, int, error) {
	raw = strings.Trim(strings.TrimSpace(raw), "`<>")
	if strings.HasPrefix(strings.ToLower(raw), "github.com/") || strings.HasPrefix(strings.ToLower(raw), "www.github.com/") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || (u.Host != "github.com" && u.Host != "www.github.com") || u.User != nil {
		return "", 0, errors.New("Use a GitHub issue link, for example https://github.com/pallets/flask/issues/6143")
	}
	m := issuePath.FindStringSubmatch(u.Path)
	if m == nil {
		return "", 0, errors.New("The issue link must include its number: https://github.com/owner/repo/issues/123")
	}
	if m[1] == "." || m[1] == ".." || m[2] == "." || m[2] == ".." {
		return "", 0, errors.New("Invalid repository")
	}
	n, _ := strconv.Atoi(m[3])
	return m[1] + "/" + m[2], n, nil
}

func issueURL(repo string, number int) string {
	return fmt.Sprintf("https://github.com/%s/issues/%d", repo, number)
}
func issueTitle(number int) string { return fmt.Sprintf("Issue #%d", number) }

func keywords(s string) []string {
	stop := map[string]bool{"this": true, "that": true, "with": true, "from": true, "when": true, "have": true, "does": true, "issue": true, "error": true, "bug": true, "the": true, "and": true, "not": true, "for": true, "are": true, "was": true, "using": true}
	seen := map[string]bool{}
	out := []string{}
	for _, w := range wordPattern.FindAllString(strings.ToLower(s), -1) {
		if !stop[w] && !seen[w] {
			out = append(out, w)
			seen[w] = true
			if len(out) == 12 {
				break
			}
		}
	}
	return out
}

type treeFile struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Type string `json:"type"`
	Mode string `json:"mode"`
	Size int    `json:"size"`
}
type GitHub struct {
	Client   *http.Client
	BaseURL  string
	Repo     string
	Number   int
	Token    string
	SHA      string
	Issue    map[string]any
	Evidence []Evidence
	Warnings []string
	Requests int
	Metrics  Metrics
	Tree     map[string]treeFile
	Files    map[string][]string
}

func newGitHub(repo string, number int, token string) *GitHub {
	return &GitHub{Client: &http.Client{Timeout: 25 * time.Second}, BaseURL: "https://api.github.com", Repo: repo, Number: number, Token: token, Evidence: []Evidence{}, Warnings: []string{}, Tree: map[string]treeFile{}, Files: map[string][]string{}}
}

func (g *GitHub) get(ctx context.Context, endpoint string, query url.Values, dst any) error {
	g.Requests++
	if g.Requests > 36 {
		return errors.New("GitHub request budget reached (36).")
	}
	u := g.BaseURL + endpoint
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "IssueLens")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return errors.New("GitHub could not be reached. Please retry.")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return errors.New("GitHub denied the request or rate-limited it. Configure a GitHub token and retry.")
	}
	if resp.StatusCode == 404 {
		return errors.New("GitHub resource not found or unavailable to this token.")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(dst)
}

func (g *GitHub) add(kind, title, link, text string) Evidence {
	for _, e := range g.Evidence {
		if e.URL == link {
			return e
		}
	}
	if len(text) > 12000 {
		text = text[:12000]
	}
	e := Evidence{ID: fmt.Sprintf("E%d", len(g.Evidence)+1), Kind: kind, Title: title, URL: link, Text: text}
	g.Evidence = append(g.Evidence, e)
	return e
}

func (g *GitHub) Initialize(ctx context.Context) error {
	var repo struct {
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.get(ctx, "/repos/"+g.Repo, nil, &repo); err != nil {
		return err
	}
	if repo.Private {
		return errors.New("This version supports public repositories only.")
	}
	var issue map[string]any
	if err := g.get(ctx, fmt.Sprintf("/repos/%s/issues/%d", g.Repo, g.Number), nil, &issue); err != nil {
		return err
	}
	if _, isPR := issue["pull_request"]; isPR {
		return errors.New("This URL is a pull request. Please choose a bug issue.")
	}
	g.Issue = issue
	title, _ := issue["title"].(string)
	body, _ := issue["body"].(string)
	if body == "" {
		body = "No description provided."
	}
	g.add("issue", title, issueURL(g.Repo, g.Number), body)
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := g.get(ctx, fmt.Sprintf("/repos/%s/issues/%d/comments", g.Repo, g.Number), url.Values{"per_page": {"20"}}, &comments); err != nil {
		return err
	}
	for _, c := range comments {
		g.add("comment", "Comment by "+c.User.Login, fmt.Sprintf("%s#issuecomment-%d", issueURL(g.Repo, g.Number), c.ID), c.Body)
	}
	if n, ok := issue["comments"].(float64); ok && n > 20 {
		g.Warnings = append(g.Warnings, "Only the first 20 issue comments were read.")
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := g.get(ctx, "/repos/"+g.Repo+"/commits/"+url.PathEscape(repo.DefaultBranch), nil, &commit); err != nil {
		return err
	}
	g.SHA = commit.SHA
	var tree struct {
		Tree      []treeFile `json:"tree"`
		Truncated bool       `json:"truncated"`
	}
	if err := g.get(ctx, "/repos/"+g.Repo+"/git/trees/"+g.SHA, url.Values{"recursive": {"1"}}, &tree); err != nil {
		return err
	}
	for _, f := range tree.Tree {
		if f.Type == "blob" && f.Mode != "120000" && f.Size <= 100000 && !strings.Contains(f.Path, "..") {
			g.Tree[f.Path] = f
		}
	}
	if tree.Truncated {
		g.Warnings = append(g.Warnings, "GitHub truncated the repository tree; code coverage is incomplete.")
	}
	return nil
}

func (g *GitHub) SearchPaths(query string) []map[string]any {
	terms := keywords(query)
	type hit struct {
		path  string
		score int
	}
	hits := []hit{}
	for p := range g.Tree {
		score := 0
		for _, t := range terms {
			if strings.Contains(strings.ToLower(p), t) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, hit{p, score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score == hits[j].score {
			return hits[i].path > hits[j].path
		}
		return hits[i].score > hits[j].score
	})
	result := []map[string]any{}
	for i, h := range hits {
		if i == 20 {
			break
		}
		result = append(result, map[string]any{"path": h.path, "score": h.score})
	}
	return result
}

func (g *GitHub) loadFile(ctx context.Context, file string) ([]string, error) {
	if lines, ok := g.Files[file]; ok {
		return lines, nil
	}
	f, ok := g.Tree[file]
	if !ok {
		return nil, errors.New("Path must come from search_paths in this repository.")
	}
	var blob struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := g.get(ctx, "/repos/"+g.Repo+"/git/blobs/"+f.SHA, nil, &blob); err != nil {
		return nil, err
	}
	if blob.Encoding != "base64" {
		return nil, errors.New("Unsupported file encoding.")
	}
	raw, err := base64.StdEncoding.DecodeString(blob.Content)
	if err != nil {
		return nil, err
	}
	if strings.ContainsRune(string(raw), 0) {
		return nil, errors.New("Binary files are not supported.")
	}
	lines := strings.Split(string(raw), "\n")
	g.Files[file] = lines
	return lines, nil
}

func (g *GitHub) ReadFile(ctx context.Context, file string, start int) (Evidence, error) {
	lines, err := g.loadFile(ctx, file)
	if err != nil {
		return Evidence{}, err
	}
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return Evidence{}, fmt.Errorf("File contains %d lines.", len(lines))
	}
	selected := []string{}
	size := 0
	for i := start - 1; i < len(lines) && i < start+99; i++ {
		line := lines[i]
		if len(line) > 1200 {
			line = line[:1200]
		}
		if size+len(line) > 10000 {
			break
		}
		selected = append(selected, fmt.Sprintf("%d: %s", i+1, line))
		size += len(line)
	}
	end := start + len(selected) - 1
	link := fmt.Sprintf("https://github.com/%s/blob/%s/%s#L%d-L%d", g.Repo, g.SHA, path.Join(strings.Split(file, "/")...), start, end)
	return g.add("code", fmt.Sprintf("%s:%d-%d", file, start, end), link, strings.Join(selected, "\n")), nil
}

func (g *GitHub) SearchCode(ctx context.Context, query string) (map[string]any, error) {
	terms := keywords(query)
	if len(terms) == 0 {
		return map[string]any{"matches": []Evidence{}, "coverage": "No searchable terms."}, nil
	}
	ext := []string{".py", ".js", ".ts", ".tsx", ".jsx", ".go", ".rs", ".java", ".kt", ".c", ".cpp", ".h", ".rb", ".php"}
	candidates := []string{}
	for p := range g.Tree {
		for _, suffix := range ext {
			if strings.HasSuffix(p, suffix) && !strings.Contains(p, "node_modules/") && !strings.Contains(p, "vendor/") {
				candidates = append(candidates, p)
				break
			}
		}
	}
	score := func(p string) int {
		n := 0
		low := strings.ToLower(p)
		for _, t := range terms {
			if strings.Contains(low, t) {
				n += 10
			}
		}
		if strings.HasPrefix(low, "src/") || strings.HasPrefix(low, "lib/") {
			n += 3
		}
		if strings.HasSuffix(low, "main.go") || strings.HasSuffix(low, "app.py") {
			n += 2
		}
		return n
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := score(candidates[i]), score(candidates[j])
		if a == b {
			return candidates[i] > candidates[j]
		}
		return a > b
	})
	selected := []string{}
	seen := map[string]bool{}
	for p := range g.Files {
		selected = append(selected, p)
		seen[p] = true
	}
	sort.Strings(selected)
	for _, p := range candidates {
		if len(selected) >= 8 {
			break
		}
		if !seen[p] {
			selected = append(selected, p)
			seen[p] = true
		}
	}
	type match struct {
		file  string
		line  int
		score int
	}
	matches := []match{}
	for _, p := range selected {
		lines, err := g.loadFile(ctx, p)
		if err != nil {
			continue
		}
		best := match{file: p}
		for i, line := range lines {
			s := 0
			for _, t := range terms {
				if strings.Contains(strings.ToLower(line), t) {
					s++
				}
			}
			if s > best.score {
				best.score = s
				best.line = i + 1
			}
		}
		if best.score > 0 {
			matches = append(matches, best)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	found := []Evidence{}
	for i, m := range matches {
		if i == 5 {
			break
		}
		e, err := g.ReadFile(ctx, m.file, max(1, m.line-8))
		if err == nil {
			found = append(found, e)
		}
	}
	coverage := fmt.Sprintf("Searched %d candidate/cached files out of %d eligible code files. This is bounded retrieval, not exhaustive repository search.", len(selected), len(candidates))
	g.Warnings = append(g.Warnings, coverage)
	return map[string]any{"matches": found, "coverage": coverage}, nil
}

func (g *GitHub) SearchIssues(ctx context.Context, query string) ([]Evidence, error) {
	terms := keywords(query)
	if len(terms) > 5 {
		terms = terms[:5]
	}
	if len(terms) == 0 {
		return []Evidence{}, nil
	}
	var result struct {
		Items []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			State  string `json:"state"`
			Body   string `json:"body"`
		} `json:"items"`
	}
	q := url.Values{"q": {"repo:" + g.Repo + " is:issue " + strings.Join(terms, " ")}, "per_page": {"6"}}
	if err := g.get(ctx, "/search/issues", q, &result); err != nil {
		return nil, err
	}
	found := []Evidence{}
	for _, i := range result.Items {
		if i.Number != g.Number {
			found = append(found, g.add("related", fmt.Sprintf("#%d %s", i.Number, i.Title), issueURL(g.Repo, i.Number), "State: "+i.State+"\n"+i.Body))
		}
	}
	return found, nil
}
