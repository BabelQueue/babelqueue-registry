package schema

import "testing"

func parse(t *testing.T, src string) *Schema {
	t.Helper()
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s
}

func TestValidate_ObjectRequiredTypesAndAdditional(t *testing.T) {
	s := parse(t, `{
		"type":"object",
		"required":["order_id"],
		"properties":{"order_id":{"type":"integer"},"note":{"type":"string","minLength":1}},
		"additionalProperties":false
	}`)

	if errs := s.Validate(map[string]any{"order_id": 7.0}); len(errs) != 0 {
		t.Fatalf("expected valid, got %v", errs)
	}
	if errs := s.Validate(map[string]any{}); len(errs) == 0 {
		t.Fatal("expected a missing-required violation")
	}
	if errs := s.Validate(map[string]any{"order_id": "x"}); len(errs) == 0 {
		t.Fatal("expected an integer-type violation")
	}
	if errs := s.Validate(map[string]any{"order_id": 7.0, "extra": 1.0}); len(errs) == 0 {
		t.Fatal("expected an additionalProperties violation")
	}
	if errs := s.Validate(map[string]any{"order_id": 7.0, "note": ""}); len(errs) == 0 {
		t.Fatal("expected a minLength violation")
	}
}

func TestValidate_EnumConstMinimumArray(t *testing.T) {
	s := parse(t, `{
		"type":"object",
		"properties":{
			"status":{"enum":["new","paid"]},
			"qty":{"type":"integer","minimum":1},
			"tags":{"type":"array","items":{"type":"string"}}
		}
	}`)

	if errs := s.Validate(map[string]any{"status": "paid", "qty": 2.0, "tags": []any{"a", "b"}}); len(errs) != 0 {
		t.Fatalf("expected valid, got %v", errs)
	}
	if errs := s.Validate(map[string]any{"status": "cancelled"}); len(errs) == 0 {
		t.Fatal("expected an enum violation")
	}
	if errs := s.Validate(map[string]any{"qty": 0.0}); len(errs) == 0 {
		t.Fatal("expected a minimum violation")
	}
	if errs := s.Validate(map[string]any{"tags": []any{"a", 1.0}}); len(errs) == 0 {
		t.Fatal("expected an array-item type violation")
	}
}

func TestValidate_IntegerVsNumber(t *testing.T) {
	s := parse(t, `{"type":"integer"}`)
	if errs := s.Validate(1.0); len(errs) != 0 {
		t.Fatalf("1.0 should validate as an integer: %v", errs)
	}
	if errs := s.Validate(1.5); len(errs) == 0 {
		t.Fatal("1.5 should fail integer validation")
	}
}

func TestValidate_Const(t *testing.T) {
	s := parse(t, `{"const":"v1"}`)
	if errs := s.Validate("v1"); len(errs) != 0 {
		t.Fatalf("matching const should validate: %v", errs)
	}
	if errs := s.Validate("v2"); len(errs) == 0 {
		t.Fatal("mismatched const should fail")
	}
}

func TestGDPRSensitive_ParseAndIgnoredByValidation(t *testing.T) {
	s := parse(t, `{
		"type":"object",
		"required":["email"],
		"properties":{
			"email":{"type":"string","x-gdpr-sensitive":"email"},
			"phone":{"type":"string","x-gdpr-sensitive":true},
			"locale":{"type":"string"},
			"opt_in":{"type":"boolean","x-gdpr-sensitive":false},
			"empty":{"type":"string","x-gdpr-sensitive":""}
		}
	}`)

	if !s.Properties["email"].GDPRSensitive || s.Properties["email"].GDPRCategory != "email" {
		t.Fatalf("string form should mark sensitive with a category: %+v", s.Properties["email"])
	}
	if !s.Properties["phone"].GDPRSensitive || s.Properties["phone"].GDPRCategory != "" {
		t.Fatalf("bool true should mark sensitive with no category: %+v", s.Properties["phone"])
	}
	if s.Properties["locale"].GDPRSensitive {
		t.Fatal("an unannotated field must not be sensitive")
	}
	if s.Properties["opt_in"].GDPRSensitive {
		t.Fatal("x-gdpr-sensitive:false must not mark sensitive")
	}
	if s.Properties["empty"].GDPRSensitive {
		t.Fatal("an empty-string category must not mark sensitive")
	}

	// The keyword must NOT affect validation: a valid value stays valid, an invalid one still fails.
	if errs := s.Validate(map[string]any{"email": "a@b.co", "phone": "x", "locale": "tr"}); len(errs) != 0 {
		t.Fatalf("x-gdpr-sensitive must not change validation, got %v", errs)
	}
	if errs := s.Validate(map[string]any{}); len(errs) == 0 {
		t.Fatal("a missing required sensitive field should still fail validation")
	}
}

func TestSensitivePaths_NestedAndArrays(t *testing.T) {
	s := parse(t, `{
		"type":"object",
		"properties":{
			"email":{"type":"string","x-gdpr-sensitive":"email"},
			"plain":{"type":"string"},
			"profile":{"type":"object","properties":{
				"full_name":{"type":"string","x-gdpr-sensitive":true}
			}},
			"addresses":{"type":"array","items":{"type":"object","properties":{
				"line":{"type":"string","x-gdpr-sensitive":true},
				"country":{"type":"string"}
			}}}
		}
	}`)

	paths := s.SensitivePaths()
	got := make([]string, len(paths))
	for i, p := range paths {
		got[i] = p.Path
	}
	want := []string{"addresses[].line", "email", "profile.full_name"}
	if len(got) != len(want) {
		t.Fatalf("SensitivePaths() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] { // SensitivePaths is sorted
			t.Fatalf("SensitivePaths() = %v, want %v", got, want)
		}
	}
}

func TestSensitivePaths_RootAndNilSafe(t *testing.T) {
	root := parse(t, `{"type":"string","x-gdpr-sensitive":true}`)
	if paths := root.SensitivePaths(); len(paths) != 1 || paths[0].Path != "" {
		t.Fatalf("a root-marked schema should yield one empty-path entry, got %v", paths)
	}
	var nilSchema *Schema
	if paths := nilSchema.SensitivePaths(); len(paths) != 0 {
		t.Fatalf("a nil schema should yield no paths, got %v", paths)
	}
}

func TestValidate_ScalarTypes(t *testing.T) {
	cases := []struct {
		src   string
		value any
		valid bool
	}{
		{`{"type":"boolean"}`, true, true},
		{`{"type":"boolean"}`, "x", false},
		{`{"type":"null"}`, nil, true},
		{`{"type":"null"}`, 1.0, false},
		{`{"type":"number","minimum":0.5}`, 0.6, true},
		{`{"type":"number","minimum":0.5}`, 0.4, false},
		{`{"type":"number"}`, "x", false},
		{`{"type":"string"}`, 5.0, false},
		{`{"type":"object"}`, "x", false},
		{`{"type":"array"}`, "x", false},
	}
	for _, c := range cases {
		errs := parse(t, c.src).Validate(c.value)
		if c.valid && len(errs) != 0 {
			t.Errorf("%s with %v: expected valid, got %v", c.src, c.value, errs)
		}
		if !c.valid && len(errs) == 0 {
			t.Errorf("%s with %v: expected a violation, got none", c.src, c.value)
		}
	}
}

// Cases mirror conformance manifest.json `payload_schema_unicode` (ADR-0024, GR-5): the
// registry does not vendor the conformance suite, so they are copied here. minLength counts
// Unicode code points — not UTF-8 bytes, not UTF-16 code units, not grapheme clusters.
func TestValidate_MinLengthCountsCodePoints(t *testing.T) {
	s := parse(t, `{
		"type":"object",
		"properties":{"min3":{"type":"string","minLength":3},"min2":{"type":"string","minLength":2}},
		"additionalProperties":false
	}`)
	cases := []struct {
		name  string
		data  map[string]any
		valid bool
	}{
		{"ascii-3cp-min3", map[string]any{"min3": "abc"}, true},
		{"turkish-2cp-min3", map[string]any{"min3": "ğü"}, false},
		{"turkish-3cp-min3-boundary", map[string]any{"min3": "ğüş"}, true},
		{"emoji-1cp-min2", map[string]any{"min2": "😀"}, false},
		{"emoji-plus-ascii-2cp-min3", map[string]any{"min3": "😀a"}, false},
		{"combining-2cp-min2", map[string]any{"min2": "e\u0301"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := s.Validate(c.data)
			if got := len(errs) == 0; got != c.valid {
				t.Fatalf("valid=%v, want %v (errors: %v)", got, c.valid, errs)
			}
		})
	}
}
