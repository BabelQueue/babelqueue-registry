package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// loadManifest reads the vendored conformance extract (testdata/conformance/manifest.json).
func loadManifest(t *testing.T, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "conformance", "manifest.json"))
	if err != nil {
		t.Fatalf("read vendored conformance manifest: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
}

// TestPayloadConformance runs the shared payload_schema cases: the registry's validator must
// agree with every SDK's payload validator on each case's `valid` flag (ADR-0024, GR-5).
func TestPayloadConformance(t *testing.T) {
	var manifest struct {
		PayloadSchema *struct {
			Schema json.RawMessage `json:"schema"`
			Cases  []struct {
				Name  string         `json:"name"`
				Valid bool           `json:"valid"`
				Data  map[string]any `json:"data"`
			} `json:"cases"`
		} `json:"payload_schema"`
	}
	loadManifest(t, &manifest)
	if manifest.PayloadSchema == nil || len(manifest.PayloadSchema.Cases) == 0 {
		t.Fatal("manifest has no payload_schema cases")
	}
	s, err := Parse(manifest.PayloadSchema.Schema)
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	for _, c := range manifest.PayloadSchema.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			if got := len(s.Validate(c.Data)) == 0; got != c.Valid {
				t.Errorf("got valid=%v, want %v", got, c.Valid)
			}
		})
	}
}

// TestPayloadUnicodeConformance runs payload_schema_unicode: minLength counts code points.
func TestPayloadUnicodeConformance(t *testing.T) {
	var manifest struct {
		PayloadSchemaUnicode *struct {
			Schema json.RawMessage `json:"schema"`
			Cases  []struct {
				Name  string         `json:"name"`
				Valid bool           `json:"valid"`
				Data  map[string]any `json:"data"`
			} `json:"cases"`
		} `json:"payload_schema_unicode"`
	}
	loadManifest(t, &manifest)
	if manifest.PayloadSchemaUnicode == nil || len(manifest.PayloadSchemaUnicode.Cases) == 0 {
		t.Fatal("manifest has no payload_schema_unicode cases")
	}
	s, err := Parse(manifest.PayloadSchemaUnicode.Schema)
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	for _, c := range manifest.PayloadSchemaUnicode.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			errs := s.Validate(c.Data)
			if got := len(errs) == 0; got != c.Valid {
				t.Errorf("got valid=%v, want %v (errors: %v)", got, c.Valid, errs)
			}
		})
	}
}

// TestPayloadKeywordsConformance runs payload_schema_keywords (K-17): union `type`, numeric
// equality in const/enum, maxLength in code points, and the registration warnings for keywords
// outside the supported subset. Each case carries its own schema; it passes when both the
// verdict and the warning set (compared as a set of {pointer, keyword}) match.
func TestPayloadKeywordsConformance(t *testing.T) {
	var manifest struct {
		PayloadSchemaKeywords *struct {
			Cases []struct {
				Name     string          `json:"name"`
				Schema   json.RawMessage `json:"schema"`
				Data     json.RawMessage `json:"data"`
				Valid    bool            `json:"valid"`
				Warnings []struct {
					Pointer string `json:"pointer"`
					Keyword string `json:"keyword"`
				} `json:"warnings"`
			} `json:"cases"`
		} `json:"payload_schema_keywords"`
	}
	loadManifest(t, &manifest)
	if manifest.PayloadSchemaKeywords == nil || len(manifest.PayloadSchemaKeywords.Cases) == 0 {
		t.Fatal("manifest has no payload_schema_keywords cases")
	}
	if n := len(manifest.PayloadSchemaKeywords.Cases); n != 66 {
		t.Fatalf("payload_schema_keywords: got %d cases, want 66", n)
	}
	for _, c := range manifest.PayloadSchemaKeywords.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			s, err := Parse(c.Schema)
			if err != nil {
				t.Fatalf("parse schema: %v", err)
			}
			var data any
			if err := json.Unmarshal(c.Data, &data); err != nil {
				t.Fatalf("decode data: %v", err)
			}
			errs := s.Validate(data)
			if got := len(errs) == 0; got != c.Valid {
				t.Errorf("got valid=%v, want %v (errors: %v)", got, c.Valid, errs)
			}

			want := map[Warning]int{}
			for _, w := range c.Warnings {
				want[Warning{Pointer: w.Pointer, Keyword: w.Keyword}]++
			}
			got := map[Warning]int{}
			for _, w := range s.Warnings() {
				got[w]++
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("warnings: got %v, want %v", s.Warnings(), c.Warnings)
			}
		})
	}
}
