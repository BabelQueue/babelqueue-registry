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

// TestBuild_CarriesGDPRSensitive proves the x-gdpr-sensitive annotation survives into the AsyncAPI
// payload, so the generated event catalog documents which fields are PII. The schema is embedded
// verbatim, so the keyword flows through untouched (string and boolean forms both).
func TestBuild_CarriesGDPRSensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "schemas/u.json"), `{
		"type":"object",
		"properties":{
			"email":{"type":"string","x-gdpr-sensitive":"email"},
			"phone":{"type":"string","x-gdpr-sensitive":true}
		}
	}`)
	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"urn:babel:users:registered","schema":"schemas/u.json"}]}`)
	r, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}

	doc, err := Build("Catalog", "1.0.0", r)
	if err != nil {
		t.Fatal(err)
	}
	comps := doc["components"].(map[string]any)
	msgs := comps["messages"].(map[string]any)
	msg := msgs["urn_babel_users_registered"].(map[string]any)
	payload := msg["payload"].(map[string]any)
	props := payload["properties"].(map[string]any)

	email := props["email"].(map[string]any)
	if email["x-gdpr-sensitive"] != "email" {
		t.Fatalf("string-form x-gdpr-sensitive not carried into AsyncAPI payload: %v", email)
	}
	phone := props["phone"].(map[string]any)
	if phone["x-gdpr-sensitive"] != true {
		t.Fatalf("bool-form x-gdpr-sensitive not carried into AsyncAPI payload: %v", phone)
	}
}
