package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type call struct {
	method, path string
	query        url.Values
	body         any
}

type fakeBackend struct {
	signedIn bool
	status   int
	reply    string
	calls    []call
}

func (f *fakeBackend) SignedIn() bool { return f.signedIn }

func (f *fakeBackend) Do(_ context.Context, method, path string, query url.Values, body any) (int, []byte, error) {
	f.calls = append(f.calls, call{method, path, query, body})
	return f.status, []byte(f.reply), nil
}

// roundTrip feeds the server one line per message and returns its answers.
func roundTrip(t *testing.T, tools *Tools, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	s := &Server{Name: "inlinr", Version: "test", Tools: tools}
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var answers []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not one JSON message per line: %q", l)
		}
		answers = append(answers, m)
	}
	return answers
}

func TestInitializeAndList(t *testing.T) {
	got := roundTrip(t, &Tools{Backend: &fakeBackend{}},
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"nope"}`,
	)
	if len(got) != 3 {
		t.Fatalf("a notification must get no answer; got %d answers", len(got))
	}
	init := got[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" {
		t.Errorf("should echo a version it knows, got %v", init["protocolVersion"])
	}
	tools := got[1]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	want := "time_summary,log_time,check_dependencies,package_releases,upgrade_notes,my_upgrades,find_usages"
	if strings.Join(names, ",") != want {
		t.Errorf("tools = %v", names)
	}
	if got[2]["error"].(map[string]any)["code"].(float64) != codeMethodNotFound {
		t.Errorf("unknown method: %v", got[2])
	}
}

func TestUnknownProtocolGetsTheLatest(t *testing.T) {
	got := roundTrip(t, &Tools{Backend: &fakeBackend{}},
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if v := got[0]["result"].(map[string]any)["protocolVersion"]; v != LatestProtocol {
		t.Errorf("got %v", v)
	}
}

func TestTimeSummaryNeedsSignIn(t *testing.T) {
	b := &fakeBackend{}
	res, _ := (&Tools{Backend: b}).Call(context.Background(), "time_summary", json.RawMessage(`{"period":"7d"}`))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "inlinr activate") {
		t.Errorf("got %+v", res)
	}
	if len(b.calls) != 0 {
		t.Error("nothing should be sent without a token")
	}
}

func TestTimeSummaryPassesTheFilters(t *testing.T) {
	b := &fakeBackend{signedIn: true, status: 200, reply: `{"total_seconds":60}`}
	res, _ := (&Tools{Backend: b}).Call(context.Background(), "time_summary",
		json.RawMessage(`{"period":"custom","from":"2026-10-01","to":"2026-10-07","project":"acme"}`))
	if res.IsError || !strings.Contains(res.Content[0].Text, `"total_seconds": 60`) {
		t.Errorf("got %+v", res)
	}
	c := b.calls[0]
	if c.path != "/api/v1/me/summary" || c.query.Get("project") != "acme" || c.query.Get("from") != "2026-10-01" {
		t.Errorf("call = %+v", c)
	}
}

func TestServerErrorsReachTheAssistant(t *testing.T) {
	b := &fakeBackend{signedIn: true, status: 400, reply: `{"error":"ambiguous_project","message":"Several of your projects have that name."}`}
	res, _ := (&Tools{Backend: b}).Call(context.Background(), "log_time", json.RawMessage(`{"minutes":30,"project":"app"}`))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "Several of your projects") {
		t.Errorf("got %+v", res)
	}
}

func TestLogTimeSendsOnlyWhatWasGiven(t *testing.T) {
	b := &fakeBackend{signedIn: true, status: 201, reply: `{"id":"e1"}`}
	(&Tools{Backend: b}).Call(context.Background(), "log_time", json.RawMessage(`{"minutes":45,"type":"meeting","billable":false}`))
	body := b.calls[0].body.(map[string]any)
	if len(body) != 3 || body["minutes"] != 45 || body["billable"] != false {
		t.Errorf("body = %v", body)
	}
	res, _ := (&Tools{Backend: b}).Call(context.Background(), "log_time", json.RawMessage(`{"label":"x"}`))
	if !res.IsError {
		t.Error("minutes is required")
	}
}

func TestCheckSendsOnlyTheDependencyBlocks(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"name":"secret-app","scripts":{"deploy":"ssh prod"},"dependencies":{"react":"^18.2.0"},"devDependencies":{"vitest":"^2"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &fakeBackend{status: 200, reply: `{"summary":{}}`}
	res, _ := (&Tools{Backend: b, Dir: dir}).Call(context.Background(), "check_dependencies", nil)
	if res.IsError {
		t.Fatalf("got %+v", res)
	}
	sent, _ := json.Marshal(b.calls[0].body)
	if strings.Contains(string(sent), "secret-app") || strings.Contains(string(sent), "ssh prod") {
		t.Errorf("sent more than the dependency blocks: %s", sent)
	}
	if !strings.Contains(string(sent), "react") || !strings.Contains(string(sent), "vitest") {
		t.Errorf("sent %s", sent)
	}
}

func TestPackageNamesCannotWalkThePath(t *testing.T) {
	b := &fakeBackend{status: 200, reply: `{}`}
	tools := &Tools{Backend: b}
	for _, name := range []string{"../me/summary", "react/../x", "", "React"} {
		res, _ := tools.Call(context.Background(), "package_releases", json.RawMessage(`{"name":"`+name+`"}`))
		if !res.IsError {
			t.Errorf("%q was accepted", name)
		}
	}
	tools.Call(context.Background(), "upgrade_notes", json.RawMessage(`{"name":"@tanstack/react-router","from":"1.0.0"}`))
	if got := b.calls[0].path; got != "/api/v1/packages/@tanstack/react-router/upgrade" {
		t.Errorf("path = %s", got)
	}
}

func TestUnknownArgumentsAreRefused(t *testing.T) {
	res, _ := (&Tools{Backend: &fakeBackend{signedIn: true}}).Call(context.Background(), "time_summary", json.RawMessage(`{"periode":"7d"}`))
	if !res.IsError {
		t.Error("a misspelt argument should not be ignored")
	}
}

func TestMyUpgradesPassesTheFilters(t *testing.T) {
	b := &fakeBackend{signedIn: true, status: 200, reply: `{"upgrades":[]}`}
	res, _ := (&Tools{Backend: b}).Call(context.Background(), "my_upgrades",
		json.RawMessage(`{"project":"acme","include_dev":false,"limit":10}`))
	if res.IsError {
		t.Fatalf("got %+v", res)
	}
	c := b.calls[0]
	if c.path != "/api/v1/me/upgrades" || c.query.Get("project") != "acme" || c.query.Get("dev") != "0" || c.query.Get("limit") != "10" {
		t.Errorf("call = %+v", c)
	}
	none := &fakeBackend{}
	res, _ = (&Tools{Backend: none}).Call(context.Background(), "my_upgrades", nil)
	if !res.IsError || len(none.calls) != 0 {
		t.Errorf("without a token: %+v", res)
	}
}

func TestFindUsagesReadsOnlyThisCodebase(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/app.tsx", "import { useState } from \"react\";\nimport { createRoot } from 'react-dom/client';\nconst x = 'react';\n")
	write("src/lazy.ts", "const jsx = await import(`react/jsx-runtime`);\nexport { forwardRef } from \"react\";\n")
	write("src/multi.ts", "import {\n  a,\n} from \"react\";\n")
	write("node_modules/react/index.js", "module.exports = require('react');\n")
	write(".next/server.js", "require(\"react\")\n")
	write("README.md", "import React from \"react\"\n")

	b := &fakeBackend{}
	res, _ := (&Tools{Backend: b, Dir: dir}).Call(context.Background(), "find_usages", json.RawMessage(`{"name":"react"}`))
	if res.IsError {
		t.Fatalf("got %+v", res)
	}
	var got struct {
		Files       int            `json:"files"`
		BySpecifier map[string]int `json:"by_specifier"`
		Usages      []Usage        `json:"usages"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, u := range got.Usages {
		lines = append(lines, u.File+":"+strconv.Itoa(u.Line))
	}
	want := "src/app.tsx:1,src/lazy.ts:1,src/lazy.ts:2,src/multi.ts:3"
	if strings.Join(lines, ",") != want {
		t.Errorf("usages = %v", lines)
	}
	if got.Files != 3 || got.BySpecifier["react/jsx-runtime"] != 1 || got.BySpecifier["react"] != 3 {
		t.Errorf("got %+v", got)
	}
	if len(b.calls) != 0 {
		t.Error("find_usages must not call the server")
	}

	res, _ = (&Tools{Backend: b, Dir: dir}).Call(context.Background(), "find_usages", json.RawMessage(`{"name":"react","max":1}`))
	if !strings.Contains(res.Content[0].Text, `"truncated": true`) {
		t.Errorf("max 1: %s", res.Content[0].Text)
	}
	res, _ = (&Tools{Backend: b, Dir: dir}).Call(context.Background(), "find_usages", json.RawMessage(`{"name":"../etc"}`))
	if !res.IsError {
		t.Error("a name that is not a package must be refused")
	}
}
