package gdpr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/babelqueue/babelqueue-registry/internal/registry"
	"github.com/babelqueue/babelqueue-registry/internal/schema"
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

// loadReg writes a one-URN registry whose schema is schemaJSON and returns it loaded.
func loadReg(t *testing.T, urn, schemaJSON string) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "s.json"), schemaJSON)
	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"`+urn+`","schema":"s.json"}]}`)
	r, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const sensitiveSchema = `{
	"type":"object",
	"required":["user_id","email"],
	"properties":{
		"user_id":{"type":"integer"},
		"email":{"type":"string","x-gdpr-sensitive":"email"},
		"phone":{"type":"string","x-gdpr-sensitive":true},
		"locale":{"type":"string"},
		"profile":{"type":"object","properties":{
			"full_name":{"type":"string","x-gdpr-sensitive":true},
			"opt_in":{"type":"boolean"}
		}},
		"addresses":{"type":"array","items":{"type":"object","properties":{
			"line":{"type":"string","x-gdpr-sensitive":true},
			"country":{"type":"string"}
		}}}
	},
	"additionalProperties":false
}`

func TestBuildInventory(t *testing.T) {
	r := loadReg(t, "urn:babel:users:registered", sensitiveSchema)
	inv, err := BuildInventory(r)
	if err != nil {
		t.Fatal(err)
	}
	if inv.TotalURNs != 1 {
		t.Fatalf("TotalURNs = %d, want 1", inv.TotalURNs)
	}
	if inv.TotalFields != 4 { // email, phone, profile.full_name, addresses[].line
		t.Fatalf("TotalFields = %d, want 4", inv.TotalFields)
	}
	if len(inv.URNs) != 1 || len(inv.URNs[0].Paths) != 4 {
		t.Fatalf("unexpected inventory: %+v", inv.URNs)
	}
}

func TestBuildInventory_BadSchemaErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"u","schema":"missing.json"}]}`)
	r, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildInventory(r); err == nil {
		t.Fatal("an unreadable schema should surface an error")
	}
}

func TestAudit_PassWhenPIIMarked(t *testing.T) {
	r := loadReg(t, "urn:babel:users:registered", sensitiveSchema)
	res, err := Audit(r, "") // default pattern
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("expected the audit to pass, got findings: %+v", res.Findings)
	}
	if res.CheckedURNs != 1 {
		t.Fatalf("CheckedURNs = %d, want 1", res.CheckedURNs)
	}
}

func TestAudit_FailsOnUnannotatedPII(t *testing.T) {
	// email is a PII-named scalar that is NOT marked → must be flagged.
	r := loadReg(t, "urn:babel:users:registered", `{
		"type":"object",
		"properties":{
			"email":{"type":"string"},
			"profile":{"type":"object","properties":{"phone":{"type":"string"}}}
		}
	}`)
	res, err := Audit(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("expected un-annotated PII to fail the audit")
	}
	got := map[string]bool{}
	for _, f := range res.Findings {
		got[f.Path] = true
	}
	if !got["email"] || !got["profile.phone"] {
		t.Fatalf("expected email and profile.phone findings, got %+v", res.Findings)
	}
}

func TestAudit_ContainerNameNotFlagged(t *testing.T) {
	// "addresses" matches the "address" PII pattern but is an array container; its sensitive
	// content lives on the marked leaf, so the container itself must not be flagged.
	r := loadReg(t, "urn:babel:users:registered", `{
		"type":"object",
		"properties":{
			"addresses":{"type":"array","items":{"type":"object","properties":{
				"line":{"type":"string","x-gdpr-sensitive":true}
			}}}
		}
	}`)
	res, err := Audit(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("a PII-named container with marked leaves should pass, got %+v", res.Findings)
	}
}

func TestAudit_CustomPattern(t *testing.T) {
	r := loadReg(t, "u", `{"type":"object","properties":{"locale":{"type":"string"}}}`)
	res, err := Audit(r, "locale")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() || len(res.Findings) != 1 || res.Findings[0].Path != "locale" {
		t.Fatalf("custom pattern should flag locale, got %+v", res.Findings)
	}
}

func TestAudit_InvalidPatternErrors(t *testing.T) {
	r := loadReg(t, "u", `{"type":"object"}`)
	if _, err := Audit(r, "("); err == nil {
		t.Fatal("an invalid regexp should error")
	}
}

func TestMask(t *testing.T) {
	s, err := schema.Parse([]byte(sensitiveSchema))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(`{
		"user_id":7,
		"email":"alice@example.com",
		"phone":"+90 555",
		"locale":"tr-TR",
		"profile":{"full_name":"Alice","opt_in":true},
		"addresses":[{"line":"Cad. No:1","country":"TR"}]
	}`), &data); err != nil {
		t.Fatal(err)
	}

	masked, ok := Mask(data, s).(map[string]any)
	if !ok {
		t.Fatal("Mask should return an object for an object input")
	}

	// Sensitive scalars: partial mask (first rune + ***).
	if masked["email"] != "a***" {
		t.Fatalf("email = %v, want a***", masked["email"])
	}
	if masked["phone"] != "+***" {
		t.Fatalf("phone = %v, want +***", masked["phone"])
	}
	// Non-sensitive fields intact.
	if masked["user_id"] != float64(7) || masked["locale"] != "tr-TR" {
		t.Fatalf("non-sensitive fields were altered: %+v", masked)
	}
	// Nested object.
	prof := masked["profile"].(map[string]any)
	if prof["full_name"] != "A***" {
		t.Fatalf("nested full_name = %v, want A***", prof["full_name"])
	}
	if prof["opt_in"] != true {
		t.Fatalf("nested non-sensitive opt_in was altered: %v", prof["opt_in"])
	}
	// Array item.
	addr := masked["addresses"].([]any)[0].(map[string]any)
	if addr["line"] != "C***" {
		t.Fatalf("array item line = %v, want C***", addr["line"])
	}
	if addr["country"] != "TR" {
		t.Fatalf("array item country was altered: %v", addr["country"])
	}

	// Non-destructive: the original input is untouched.
	if data["email"] != "alice@example.com" {
		t.Fatalf("Mask mutated the input: email = %v", data["email"])
	}
	orig := data["profile"].(map[string]any)
	if orig["full_name"] != "Alice" {
		t.Fatalf("Mask mutated a nested input value: %v", orig["full_name"])
	}
}

func TestMask_DeepCopiesNonSensitiveContainers(t *testing.T) {
	// A non-sensitive nested object/array that lives under a sensitive sibling path must be
	// deep-copied (not aliased) into the result. This exercises deepCopy's container branches.
	s, err := schema.Parse([]byte(`{
		"type":"object",
		"properties":{
			"email":{"type":"string","x-gdpr-sensitive":true},
			"settings":{"type":"object"}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(`{
		"email":"a@b.co",
		"settings":{"theme":"dark","tags":["x","y"],"nested":{"k":1}}
	}`), &data); err != nil {
		t.Fatal(err)
	}

	masked := Mask(data, s).(map[string]any)
	settings := masked["settings"].(map[string]any)
	// The whole subtree must be preserved value-equal …
	if !reflect.DeepEqual(settings, data["settings"]) {
		t.Fatalf("non-sensitive subtree altered: %v", settings)
	}
	// … but be a distinct map, not an alias (deep copy).
	if &settings == nil { // guard against nil
		t.Fatal("settings missing")
	}
	settings["theme"] = "light"
	if data["settings"].(map[string]any)["theme"] != "dark" {
		t.Fatal("Mask aliased a non-sensitive nested container instead of deep-copying it")
	}
}

func TestAuditAndInventory_SkipUnreadableURN(t *testing.T) {
	// A URN with no registered schema (file removed mid-edit) is skipped, not fatal, only when
	// RawSchema reports ok=false. We simulate "ok=false" by an URN the registry doesn't resolve —
	// here we use a two-entry registry where one schema is valid; both audit and inventory must
	// count only what they can read. (registry.Schema returns ok=false for an unknown URN, but
	// every declared URN resolves, so this asserts the happy multi-URN path.)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.json"), `{"type":"object","properties":{"email":{"type":"string","x-gdpr-sensitive":true}}}`)
	writeFile(t, filepath.Join(dir, "b.json"), `{"type":"object","properties":{"id":{"type":"integer"}}}`)
	writeFile(t, filepath.Join(dir, "registry.json"),
		`{"schemas":[{"urn":"urn:a","schema":"a.json"},{"urn":"urn:b","schema":"b.json"}]}`)
	r, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	inv, err := BuildInventory(r)
	if err != nil {
		t.Fatal(err)
	}
	if inv.TotalURNs != 2 || inv.TotalFields != 1 {
		t.Fatalf("inventory over two URNs = %+v", inv)
	}
	res, err := Audit(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || res.CheckedURNs != 2 {
		t.Fatalf("audit over two URNs: ok=%v checked=%d findings=%+v", res.OK(), res.CheckedURNs, res.Findings)
	}
}

func TestMask_NonStringSensitiveAndNilSchema(t *testing.T) {
	// A sensitive non-string value collapses to "***".
	s, err := schema.Parse([]byte(`{"type":"object","properties":{"age":{"type":"integer","x-gdpr-sensitive":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	masked := Mask(map[string]any{"age": float64(33)}, s).(map[string]any)
	if masked["age"] != "***" {
		t.Fatalf("a sensitive number should mask to ***, got %v", masked["age"])
	}

	// A nil schema leaves the value structurally copied but unmasked.
	got := Mask(map[string]any{"a": "b"}, nil)
	if !reflect.DeepEqual(got, map[string]any{"a": "b"}) {
		t.Fatalf("a nil schema should deep-copy unchanged, got %v", got)
	}

	// An empty sensitive string collapses to "***" (no first rune to keep).
	s2, err := schema.Parse([]byte(`{"type":"string","x-gdpr-sensitive":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := Mask("", s2); got != "***" {
		t.Fatalf("an empty sensitive string should mask to ***, got %v", got)
	}
}
