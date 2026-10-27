package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// Backend is the Inlinr API as the tools see it. HTTPBackend is the real one;
// tests pass a fake.
type Backend interface {
	// Do sends a request to an /api/v1 path and returns the status and body.
	Do(ctx context.Context, method, path string, query url.Values, body any) (int, []byte, error)
	// SignedIn says whether a device token is configured.
	SignedIn() bool
}

// HTTPBackend calls https://inlinr.com with the machine's device token.
type HTTPBackend struct {
	BaseURL   string
	Token     string
	UserAgent string
	HTTP      *http.Client
}

func (b *HTTPBackend) SignedIn() bool { return b.Token != "" }

func (b *HTTPBackend) Do(ctx context.Context, method, path string, query url.Values, body any) (int, []byte, error) {
	u := b.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.Token)
	}
	req.Header.Set("User-Agent", b.UserAgent)
	client := b.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, out, err
}

// Tool is one entry of tools/list.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

// Result is a tools/call answer: the API's JSON as text, or what went wrong
// with isError set, so the assistant can tell the user rather than retry.
type Result struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Tools holds the tool set and what it calls.
type Tools struct {
	Backend Backend
	// Dir is where check_dependencies looks for package.json by default.
	Dir string
}

type handler func(t *Tools, ctx context.Context, args json.RawMessage) Result

type toolDef struct {
	Tool
	run handler
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var readOnly = map[string]any{"readOnlyHint": true, "openWorldHint": true}

var defs = []toolDef{
	{
		Tool: Tool{
			Name:  "time_summary",
			Title: "Time and AI usage",
			Description: "The user's tracked coding time for a period, by project and by language, and their AI assistants' time, " +
				"share of lines, tokens and estimated cost, by tool. Use it for \"how long did I spend on X\" or \"what did Claude cost me this month\".",
			InputSchema: obj(map[string]any{
				"period": map[string]any{
					"type":        "string",
					"description": "today, yesterday, 7d (default), 14d, week, month, lastMonth, 30d… or custom with from and to.",
				},
				"from":    str("Start date, YYYY-MM-DD, with period=custom."),
				"to":      str("End date, YYYY-MM-DD, with period=custom."),
				"project": str("A project name or its git remote. Leave it out for every project."),
			}),
			Annotations: readOnly,
		},
		run: (*Tools).timeSummary,
	},
	{
		Tool: Tool{
			Name:  "log_time",
			Title: "Log a manual entry",
			Description: "Records time spent outside the editor (a meeting, a call, a review) as a manual entry in Inlinr. " +
				"Ask the user before logging time they did not mention.",
			InputSchema: obj(map[string]any{
				"minutes": map[string]any{"type": "integer", "minimum": 1, "maximum": 1440, "description": "How long, in minutes."},
				"type": map[string]any{
					"type": "string",
					"enum": []string{"meeting", "call", "review", "doc", "research", "testing", "email", "custom"},
				},
				"label":      str("What it was, in a few words."),
				"started_at": str("When it started, ISO 8601. Defaults to `minutes` ago."),
				"billable":   map[string]any{"type": "boolean", "description": "Billable to a client. Defaults to false."},
				"project":    str("A project name or its git remote."),
			}, "minutes"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
		run: (*Tools).logTime,
	},
	{
		Tool: Tool{
			Name:  "check_dependencies",
			Title: "Check a package.json",
			Description: "Checks npm dependencies against the registry and OSV: how far each is behind, deprecations, and security advisories with the version that fixes them. " +
				"Reads ./package.json by default; only its dependency blocks are sent. A range that cannot be read exactly (workspace:*, git URLs, latest) is reported as unchecked, never as up to date.",
			InputSchema: obj(map[string]any{
				"path": str("Path to a package.json. Defaults to the one in the current folder."),
				"packages": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Instead of a file: names with an optional range, like react@^18.2.0.",
				},
			}),
			Annotations: readOnly,
		},
		run: (*Tools).checkDependencies,
	},
	{
		Tool: Tool{
			Name:        "package_releases",
			Title:       "Package releases",
			Description: "An npm package's recent releases with their release notes, and its security advisories.",
			InputSchema: obj(map[string]any{
				"name":        str("The npm package name, scoped or not."),
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Releases to return, newest first. Default 30."},
				"since":       str("Only releases published since this ISO date."),
				"prereleases": map[string]any{"type": "boolean", "description": "Include release candidates and betas."},
			}, "name"),
			Annotations: readOnly,
		},
		run: (*Tools).packageReleases,
	},
	{
		Tool: Tool{
			Name:  "upgrade_notes",
			Title: "What changes in an upgrade",
			Description: "What changes between two versions of an npm package, read from every release note in between: " +
				"breaking changes, deprecations, advisories fixed and still open, features, fixes, and migration guides.",
			InputSchema: obj(map[string]any{
				"name": str("The npm package name."),
				"from": str("The version installed now."),
				"to":   str("The version to upgrade to. Defaults to the latest."),
			}, "name", "from"),
			Annotations: readOnly,
		},
		run: (*Tools).upgradeNotes,
	},
	{
		Tool: Tool{
			Name:  "my_upgrades",
			Title: "What to upgrade",
			Description: "The user's Upgrades list from Inlinr, for the projects whose dependencies they follow: security fixes first, then deprecated packages, " +
				"majors and the rest, each with the version to go from and to, the projects that declare it and the page that says what changes. " +
				"Use it for \"what should I upgrade\"; then upgrade_notes for one package, and find_usages to see where the code uses it.",
			InputSchema: obj(map[string]any{
				"project":     str("A project name or its git remote. Leave it out for every followed project."),
				"include_dev": map[string]any{"type": "boolean", "description": "Count dev dependencies. Default true."},
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "Upgrades to return, most urgent first. Default 50."},
			}),
			Annotations: readOnly,
		},
		run: (*Tools).myUpgrades,
	},
	{
		Tool: Tool{
			Name:  "find_usages",
			Title: "Where a package is used",
			Description: "Lists the lines of this codebase that import or require an npm package (or one of its subpaths), so an upgrade can be checked against them. " +
				"Searched on this machine only; nothing is sent. Skips node_modules, build output and hidden folders.",
			InputSchema: obj(map[string]any{
				"name": str("The npm package name."),
				"dir":  str("Folder to search, relative to the one the assistant runs in. Defaults to that folder."),
				"max":  map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "description": "Lines to return. Default 200."},
			}, "name"),
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		run: (*Tools).findUsages,
	},
}

// List returns the tools for tools/list.
func (t *Tools) List() []Tool {
	out := make([]Tool, len(defs))
	for i, d := range defs {
		out[i] = d.Tool
	}
	return out
}

// Call runs a tool by name; ok is false for a name we do not have.
func (t *Tools) Call(ctx context.Context, name string, args json.RawMessage) (Result, bool) {
	for _, d := range defs {
		if d.Name == name {
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			return d.run(t, ctx, args), true
		}
	}
	return Result{}, false
}

func failure(format string, a ...any) Result {
	return Result{Content: []Content{{Type: "text", Text: fmt.Sprintf(format, a...)}}, IsError: true}
}

const signInFirst = "This machine is not signed in to Inlinr. Run `inlinr activate` in a terminal, then try again."

// answer turns an API reply into a tool result: the JSON itself when it
// worked, the server's message when it did not.
func (t *Tools) answer(ctx context.Context, method, path string, query url.Values, body any) Result {
	status, out, err := t.Backend.Do(ctx, method, path, query, body)
	if err != nil {
		return failure("Could not reach Inlinr: %v", err)
	}
	if status >= 200 && status < 300 {
		var v any
		if json.Unmarshal(out, &v) == nil {
			pretty, _ := json.MarshalIndent(v, "", "  ")
			return Result{Content: []Content{{Type: "text", Text: string(pretty)}}}
		}
		return Result{Content: []Content{{Type: "text", Text: string(out)}}}
	}
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(out, &e)
	switch {
	case status == http.StatusUnauthorized:
		return failure("Inlinr refused this machine's token. Run `inlinr activate` again.")
	case e.Message != "":
		return failure("%s (%s)", e.Message, e.Error)
	case e.Error != "":
		return failure("Inlinr answered %d: %s", status, e.Error)
	default:
		return failure("Inlinr answered %d.", status)
	}
}

func decode(args json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (t *Tools) timeSummary(ctx context.Context, args json.RawMessage) Result {
	var a struct {
		Period  string `json:"period"`
		From    string `json:"from"`
		To      string `json:"to"`
		Project string `json:"project"`
	}
	if err := decode(args, &a); err != nil {
		return failure("Bad arguments: %v", err)
	}
	if !t.Backend.SignedIn() {
		return failure(signInFirst)
	}
	q := url.Values{}
	for k, v := range map[string]string{"period": a.Period, "from": a.From, "to": a.To, "project": a.Project} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return t.answer(ctx, http.MethodGet, "/api/v1/me/summary", q, nil)
}

func (t *Tools) logTime(ctx context.Context, args json.RawMessage) Result {
	var a struct {
		Minutes   *int   `json:"minutes"`
		Type      string `json:"type"`
		Label     string `json:"label"`
		StartedAt string `json:"started_at"`
		Billable  *bool  `json:"billable"`
		Project   string `json:"project"`
	}
	if err := decode(args, &a); err != nil {
		return failure("Bad arguments: %v", err)
	}
	if a.Minutes == nil {
		return failure("minutes is required.")
	}
	if !t.Backend.SignedIn() {
		return failure(signInFirst)
	}
	body := map[string]any{"minutes": *a.Minutes}
	if a.Type != "" {
		body["type"] = a.Type
	}
	if a.Label != "" {
		body["label"] = a.Label
	}
	if a.StartedAt != "" {
		body["started_at"] = a.StartedAt
	}
	if a.Billable != nil {
		body["billable"] = *a.Billable
	}
	if a.Project != "" {
		body["project"] = a.Project
	}
	return t.answer(ctx, http.MethodPost, "/api/v1/me/entries", nil, body)
}

// The four blocks a check reads; nothing else in a package.json leaves the
// machine (no scripts, no name, no repository).
var manifestBlocks = []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"}

func (t *Tools) checkDependencies(ctx context.Context, args json.RawMessage) Result {
	var a struct {
		Path     string   `json:"path"`
		Packages []string `json:"packages"`
	}
	if err := decode(args, &a); err != nil {
		return failure("Bad arguments: %v", err)
	}
	if len(a.Packages) > 0 {
		return t.answer(ctx, http.MethodPost, "/api/v1/check", nil, map[string]any{"packages": a.Packages})
	}
	path := a.Path
	if path == "" {
		path = filepath.Join(t.Dir, "package.json")
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(t.Dir, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return failure("Could not read %s: %v", path, err)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return failure("%s is not valid JSON: %v", path, err)
	}
	body := map[string]json.RawMessage{}
	for _, k := range manifestBlocks {
		if v, ok := manifest[k]; ok {
			body[k] = v
		}
	}
	if len(body) == 0 {
		return failure("%s declares no dependencies.", path)
	}
	return t.answer(ctx, http.MethodPost, "/api/v1/check", nil, body)
}

// npm's own rule for a package name, so a name cannot walk the URL path.
var packageName = regexp.MustCompile(`^(@[a-z0-9-~][a-z0-9-._~]*/)?[a-z0-9-~][a-z0-9-._~]*$`)

func (t *Tools) packageReleases(ctx context.Context, args json.RawMessage) Result {
	var a struct {
		Name        string `json:"name"`
		Limit       int    `json:"limit"`
		Since       string `json:"since"`
		Prereleases bool   `json:"prereleases"`
	}
	if err := decode(args, &a); err != nil {
		return failure("Bad arguments: %v", err)
	}
	if !packageName.MatchString(a.Name) {
		return failure("%q is not an npm package name.", a.Name)
	}
	q := url.Values{}
	if a.Limit > 0 {
		q.Set("limit", strconv.Itoa(a.Limit))
	}
	if a.Since != "" {
		q.Set("since", a.Since)
	}
	if a.Prereleases {
		q.Set("prereleases", "1")
	}
	return t.answer(ctx, http.MethodGet, "/api/v1/packages/"+a.Name+"/releases", q, nil)
}

func (t *Tools) upgradeNotes(ctx context.Context, args json.RawMessage) Result {
	var a struct {
		Name string `json:"name"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decode(args, &a); err != nil {
		return failure("Bad arguments: %v", err)
	}
	if !packageName.MatchString(a.Name) {
		return failure("%q is not an npm package name.", a.Name)
	}
	if a.From == "" {
		return failure("from is required: the version installed now.")
	}
	q := url.Values{"from": {a.From}}
	if a.To != "" {
		q.Set("to", a.To)
	}
	return t.answer(ctx, http.MethodGet, "/api/v1/packages/"+a.Name+"/upgrade", q, nil)
}

func (t *Tools) myUpgrades(ctx context.Context, args json.RawMessage) Result {
	var a struct {
		Project    string `json:"project"`
		IncludeDev *bool  `json:"include_dev"`
		Limit      int    `json:"limit"`
	}
	if err := decode(args, &a); err != nil {
		return failure("Bad arguments: %v", err)
	}
	if !t.Backend.SignedIn() {
		return failure(signInFirst)
	}
	q := url.Values{}
	if a.Project != "" {
		q.Set("project", a.Project)
	}
	if a.IncludeDev != nil && !*a.IncludeDev {
		q.Set("dev", "0")
	}
	if a.Limit > 0 {
		q.Set("limit", strconv.Itoa(a.Limit))
	}
	return t.answer(ctx, http.MethodGet, "/api/v1/me/upgrades", q, nil)
}
