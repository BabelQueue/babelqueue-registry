package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunCompat(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.json")
	neu := filepath.Join(dir, "new.json")
	write(t, old, `{"type":"object","properties":{"a":{"type":"string"}}}`)

	// Additive optional → compatible (exit 0).
	write(t, neu, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}`)
	if code := runCompat([]string{old, neu}); code != 0 {
		t.Fatalf("compatible change should exit 0, got %d", code)
	}

	// New required field → breaking (exit 1).
	write(t, neu, `{"type":"object","required":["b"],"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`)
	if code := runCompat([]string{old, neu}); code != 1 {
		t.Fatalf("breaking change should exit 1, got %d", code)
	}

	// Wrong arg count → usage error (exit 2).
	if code := runCompat([]string{old}); code != 2 {
		t.Fatalf("missing arg should exit 2, got %d", code)
	}
}

func TestRunValidateAndCheck(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "schemas/o.json"),
		`{"type":"object","required":["order_id"],"properties":{"order_id":{"type":"integer"}}}`)
	reg := filepath.Join(dir, "registry.json")
	write(t, reg, `{"schemas":[{"urn":"urn:babel:orders:created","schema":"schemas/o.json"}]}`)

	good := filepath.Join(dir, "good.json")
	write(t, good, `{"job":"urn:babel:orders:created","data":{"order_id":7}}`)
	if code := runValidate([]string{"--registry", reg, good}); code != 0 {
		t.Fatalf("valid envelope should exit 0, got %d", code)
	}

	bad := filepath.Join(dir, "bad.json")
	write(t, bad, `{"job":"urn:babel:orders:created","data":{}}`)
	if code := runValidate([]string{"--registry", reg, bad}); code != 1 {
		t.Fatalf("invalid envelope should exit 1, got %d", code)
	}

	unknown := filepath.Join(dir, "unknown.json")
	write(t, unknown, `{"job":"urn:babel:unknown","data":{}}`)
	if code := runValidate([]string{"--registry", reg, unknown}); code != 0 {
		t.Fatalf("an unregistered urn should be skipped (exit 0), got %d", code)
	}

	if code := runValidate([]string{"--registry", reg}); code != 2 {
		t.Fatalf("no input files should exit 2, got %d", code)
	}

	if code := runCheck([]string{"--registry", reg}); code != 0 {
		t.Fatalf("check on a valid registry should exit 0, got %d", code)
	}
}

func TestRunCompat_FileErrors(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "g.json")
	write(t, good, `{"type":"object"}`)
	missing := filepath.Join(dir, "missing.json")

	if code := runCompat([]string{missing, good}); code != 2 {
		t.Fatalf("missing old schema → exit 2, got %d", code)
	}
	if code := runCompat([]string{good, missing}); code != 2 {
		t.Fatalf("missing new schema → exit 2, got %d", code)
	}
	badJSON := filepath.Join(dir, "bad.json")
	write(t, badJSON, `not json`)
	if code := runCompat([]string{badJSON, good}); code != 2 {
		t.Fatalf("invalid schema JSON → exit 2, got %d", code)
	}
}

func TestRunValidate_ErrorsAndSkips(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "schemas/o.json"),
		`{"type":"object","required":["x"],"properties":{"x":{"type":"integer"}}}`)
	reg := filepath.Join(dir, "registry.json")
	write(t, reg, `{"schemas":[{"urn":"urn:babel:orders:created","schema":"schemas/o.json"}]}`)

	if code := runValidate([]string{"--registry", filepath.Join(dir, "nope.json"), "x"}); code != 2 {
		t.Fatalf("missing registry → exit 2, got %d", code)
	}
	if code := runValidate([]string{"--registry", reg, filepath.Join(dir, "nope.json")}); code != 2 {
		t.Fatalf("missing envelope → exit 2, got %d", code)
	}
	badJSON := filepath.Join(dir, "badj.json")
	write(t, badJSON, `not json`)
	if code := runValidate([]string{"--registry", reg, badJSON}); code != 2 {
		t.Fatalf("invalid envelope JSON → exit 2, got %d", code)
	}
	noJob := filepath.Join(dir, "nojob.json")
	write(t, noJob, `{"data":{}}`)
	if code := runValidate([]string{"--registry", reg, noJob}); code != 0 {
		t.Fatalf("no job/urn → skipped (exit 0), got %d", code)
	}
	alias := filepath.Join(dir, "alias.json")
	write(t, alias, `{"urn":"urn:babel:orders:created","data":{"x":1}}`)
	if code := runValidate([]string{"--registry", reg, alias}); code != 0 {
		t.Fatalf("urn alias, valid data → exit 0, got %d", code)
	}
}

func TestRunCheck_Errors(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry.json")
	write(t, reg, `{"schemas":[{"urn":"u","schema":"missing.json"}]}`)
	if code := runCheck([]string{"--registry", reg}); code != 1 {
		t.Fatalf("registry with a missing schema → exit 1, got %d", code)
	}
	if code := runCheck([]string{"--registry", filepath.Join(dir, "nope.json")}); code != 2 {
		t.Fatalf("missing registry → exit 2, got %d", code)
	}
}

func TestRunServe(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "schemas/o.json"),
		`{"type":"object","properties":{"order_id":{"type":"integer"}}}`)
	reg := filepath.Join(dir, "registry.json")
	write(t, reg, `{"schemas":[{"urn":"urn:babel:orders:created","schema":"schemas/o.json"}]}`)

	// Substitute the listen seam so runServe does not block; capture what it was handed.
	orig := listenAndServe
	t.Cleanup(func() { listenAndServe = orig })

	var gotAddr string
	var gotHandler http.Handler
	listenAndServe = func(addr string, h http.Handler) error {
		gotAddr = addr
		gotHandler = h
		return nil // simulate a clean shutdown so runServe returns 0.
	}
	if code := runServe([]string{"--registry", reg, "--addr", "127.0.0.1:0"}); code != 0 {
		t.Fatalf("serve on a valid registry should exit 0, got %d", code)
	}
	if gotAddr != "127.0.0.1:0" {
		t.Fatalf("addr passed to listener = %q, want 127.0.0.1:0", gotAddr)
	}
	if gotHandler == nil {
		t.Fatal("serve did not hand a handler to the listener")
	}

	// A bind/serve failure surfaces as an IO error (exit 2).
	listenAndServe = func(string, http.Handler) error { return errors.New("bind failed") }
	if code := runServe([]string{"--registry", reg}); code != 2 {
		t.Fatalf("a listen failure should exit 2, got %d", code)
	}

	// A missing registry never reaches the listener — exit 2.
	if code := runServe([]string{"--registry", filepath.Join(dir, "nope.json")}); code != 2 {
		t.Fatalf("missing registry → exit 2, got %d", code)
	}
}

func TestRunExport(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "schemas/o.json"),
		`{"type":"object","properties":{"order_id":{"type":"integer"}}}`)
	reg := filepath.Join(dir, "registry.json")
	write(t, reg, `{"schemas":[{"urn":"urn:babel:orders:created","schema":"schemas/o.json"}]}`)

	out := filepath.Join(dir, "asyncapi.json")
	if code := runExport([]string{"--registry", reg, "-o", out}); code != 0 {
		t.Fatalf("export to file should exit 0, got %d", code)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"asyncapi": "3.0.0"`) {
		t.Fatal("output is not an AsyncAPI 3.0 document")
	}
	if !strings.Contains(string(data), "urn:babel:orders:created") {
		t.Fatal("the urn is missing from the export")
	}

	if code := runExport([]string{"--registry", reg}); code != 0 {
		t.Fatalf("export to stdout should exit 0, got %d", code)
	}
	if code := runExport([]string{"--registry", filepath.Join(dir, "nope.json")}); code != 2 {
		t.Fatalf("missing registry → exit 2, got %d", code)
	}
}
