package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAndSchema(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "schemas/orders-created.json"),
		`{"type":"object","required":["order_id"],"properties":{"order_id":{"type":"integer"}}}`)
	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"urn:babel:orders:created","schema":"schemas/orders-created.json","owner":"orders"}]}`)

	r, err := Load(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Has("urn:babel:orders:created") {
		t.Fatal("urn should be present")
	}
	if got := r.URNs(); len(got) != 1 {
		t.Fatalf("URNs() = %v, want 1", got)
	}

	s, ok, err := r.Schema("urn:babel:orders:created")
	if err != nil || !ok {
		t.Fatalf("Schema: ok=%v err=%v", ok, err)
	}
	if errs := s.Validate(map[string]any{"order_id": 7.0}); len(errs) != 0 {
		t.Fatalf("a valid payload was rejected: %v", errs)
	}

	if _, ok, _ := r.Schema("urn:babel:unknown"); ok {
		t.Fatal("an unknown urn should return ok=false")
	}
}

func TestLoad_DuplicateURNFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"u","schema":"a.json"},{"urn":"u","schema":"b.json"}]}`)
	if _, err := Load(filepath.Join(dir, "registry.json")); err == nil {
		t.Fatal("a duplicate urn should error")
	}
}

func TestLoad_EmptyFieldsFail(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"","schema":"a.json"}]}`)
	if _, err := Load(filepath.Join(dir, "registry.json")); err == nil {
		t.Fatal("an empty urn should error")
	}

	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"u","schema":""}]}`)
	if _, err := Load(filepath.Join(dir, "registry.json")); err == nil {
		t.Fatal("an empty schema path should error")
	}
}

func TestLoad_MissingOrInvalidManifestFails(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("a missing manifest should error")
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "registry.json"), `not json`)
	if _, err := Load(filepath.Join(dir, "registry.json")); err == nil {
		t.Fatal("an invalid manifest should error")
	}
}

func TestSchema_MissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"u","schema":"missing.json"}]}`)
	r, err := Load(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := r.Schema("u")
	if !ok || err == nil {
		t.Fatalf("a missing schema file should report ok=true with an error; got ok=%v err=%v", ok, err)
	}
}
