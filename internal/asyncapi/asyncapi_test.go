package asyncapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/babelqueue/babelqueue-registry/internal/registry"
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

func TestBuild(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "schemas/o.json"),
		`{"type":"object","required":["order_id"],"properties":{"order_id":{"type":"integer"}}}`)
	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"urn:babel:orders:created","schema":"schemas/o.json"}]}`)
	r, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}

	doc, err := Build("Catalog", "1.0.0", r)
	if err != nil {
		t.Fatal(err)
	}
	if doc["asyncapi"] != "3.0.0" {
		t.Fatalf("asyncapi = %v, want 3.0.0", doc["asyncapi"])
	}

	channels, ok := doc["channels"].(map[string]any)
	if !ok {
		t.Fatal("channels missing")
	}
	if _, ok := channels["urn:babel:orders:created"]; !ok {
		t.Fatal("a channel for the urn is missing")
	}

	comps := doc["components"].(map[string]any)
	msgs := comps["messages"].(map[string]any)
	msg, ok := msgs["urn_babel_orders_created"].(map[string]any)
	if !ok {
		t.Fatalf("sanitized message component missing: %v", msgs)
	}
	if msg["name"] != "urn:babel:orders:created" {
		t.Fatalf("message name = %v", msg["name"])
	}
	payload, ok := msg["payload"].(map[string]any)
	if !ok || payload["type"] != "object" {
		t.Fatalf("the data schema was not embedded as payload: %v", msg["payload"])
	}
}
