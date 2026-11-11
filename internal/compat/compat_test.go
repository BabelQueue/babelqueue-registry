package compat

import (
	"testing"

	"github.com/babelqueue/babelqueue-registry/internal/schema"
)

func parse(t *testing.T, src string) *schema.Schema {
	t.Helper()
	s, err := schema.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s
}

func TestCheck_AdditiveOptionalIsCompatible(t *testing.T) {
	old := parse(t, `{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`)
	neu := parse(t, `{"type":"object","required":["a"],"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`)
	if breaks := Check(old, neu); len(breaks) != 0 {
		t.Fatalf("an additive optional field must be compatible, got %v", breaks)
	}
}

func TestCheck_NewRequiredIsBreaking(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"a":{"type":"string"}}}`)
	neu := parse(t, `{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`)
	if breaks := Check(old, neu); len(breaks) == 0 {
		t.Fatal("making an optional field required must be breaking")
	}
}

func TestCheck_TypeChangeIsBreaking(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"a":{"type":"string"}}}`)
	neu := parse(t, `{"type":"object","properties":{"a":{"type":"integer"}}}`)
	if breaks := Check(old, neu); len(breaks) == 0 {
		t.Fatal("retyping a property must be breaking")
	}
}

func TestCheck_EnumNarrowingIsBreakingWideningIsNot(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"s":{"enum":["a","b"]}}}`)
	narrow := parse(t, `{"type":"object","properties":{"s":{"enum":["a"]}}}`)
	if breaks := Check(old, narrow); len(breaks) == 0 {
		t.Fatal("dropping an enum value must be breaking")
	}
	wide := parse(t, `{"type":"object","properties":{"s":{"enum":["a","b","c"]}}}`)
	if breaks := Check(old, wide); len(breaks) != 0 {
		t.Fatalf("widening an enum must be compatible, got %v", breaks)
	}
}

func TestCheck_AdditionalPropertiesTightenedIsBreaking(t *testing.T) {
	old := parse(t, `{"type":"object","additionalProperties":true}`)
	neu := parse(t, `{"type":"object","additionalProperties":false}`)
	if breaks := Check(old, neu); len(breaks) == 0 {
		t.Fatal("tightening additionalProperties to false must be breaking")
	}
}

func TestCheck_RemovedPropertyUnderClosedIsBreaking(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"additionalProperties":false}`)
	neu := parse(t, `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`)
	breaks := Check(old, neu)
	if len(breaks) != 1 || breaks[0] != "b: property removed" {
		t.Fatalf("removing a property while additionalProperties is false must be breaking, got %v", breaks)
	}
}

func TestCheck_RemovedPropertyUnderOpenIsBreaking(t *testing.T) {
	// additionalProperties absent (open) and explicitly true must both flag a removal.
	for _, ap := range []string{``, `,"additionalProperties":true`} {
		old := parse(t, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}`+ap+`}`)
		neu := parse(t, `{"type":"object","properties":{"a":{"type":"string"}}`+ap+`}`)
		breaks := Check(old, neu)
		if len(breaks) != 1 || breaks[0] != "b: property removed" {
			t.Fatalf("removing a property from an open schema (%q) must be breaking, got %v", ap, breaks)
		}
	}
}

func TestCheck_NestedRemovedPropertyIsBreakingWithPath(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"customer":{"type":"object","properties":{"id":{"type":"integer"},"email":{"type":"string"}}}}}`)
	neu := parse(t, `{"type":"object","properties":{"customer":{"type":"object","properties":{"id":{"type":"integer"}}}}}`)
	breaks := Check(old, neu)
	if len(breaks) != 1 || breaks[0] != "customer.email: property removed" {
		t.Fatalf("a nested removal must be reported with its path, got %v", breaks)
	}

	oldArr := parse(t, `{"type":"object","properties":{"lines":{"type":"array","items":{"type":"object","properties":{"sku":{"type":"string"},"qty":{"type":"integer"}}}}}}`)
	neuArr := parse(t, `{"type":"object","properties":{"lines":{"type":"array","items":{"type":"object","properties":{"sku":{"type":"string"}}}}}}`)
	breaks = Check(oldArr, neuArr)
	if len(breaks) != 1 || breaks[0] != "lines[].qty: property removed" {
		t.Fatalf("a removal inside array items must be reported with its path, got %v", breaks)
	}
}

func TestCheck_NestedObjectRetypeIsBreaking(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"addr":{"type":"object","properties":{"zip":{"type":"string"}}}}}`)
	neu := parse(t, `{"type":"object","properties":{"addr":{"type":"object","properties":{"zip":{"type":"integer"}}}}}`)
	if breaks := Check(old, neu); len(breaks) == 0 {
		t.Fatal("retyping a nested property must be breaking")
	}
}

func TestCheck_ArrayItemRetypeIsBreaking(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}`)
	neu := parse(t, `{"type":"object","properties":{"tags":{"type":"array","items":{"type":"integer"}}}}`)
	if breaks := Check(old, neu); len(breaks) == 0 {
		t.Fatal("retyping an array's item type must be breaking")
	}
}

func TestCheck_TightenedConstraintsAreBreaking(t *testing.T) {
	oldMin := parse(t, `{"type":"object","properties":{"n":{"type":"integer","minimum":0}}}`)
	newMin := parse(t, `{"type":"object","properties":{"n":{"type":"integer","minimum":5}}}`)
	if breaks := Check(oldMin, newMin); len(breaks) == 0 {
		t.Fatal("raising minimum must be breaking")
	}

	oldLen := parse(t, `{"type":"object","properties":{"s":{"type":"string"}}}`)
	newLen := parse(t, `{"type":"object","properties":{"s":{"type":"string","minLength":3}}}`)
	if breaks := Check(oldLen, newLen); len(breaks) == 0 {
		t.Fatal("adding minLength must be breaking")
	}
}

func TestCheck_EnumAddedWhereAnyAllowedIsBreaking(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"s":{"type":"string"}}}`)
	neu := parse(t, `{"type":"object","properties":{"s":{"type":"string","enum":["a","b"]}}}`)
	if breaks := Check(old, neu); len(breaks) == 0 {
		t.Fatal("adding an enum where any value was allowed must be breaking")
	}
}

func TestCheck_UnionTypeAddingIsCompatible(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":["string","null"]}}}`)
	neu := parse(t, `{"type":"object","properties":{"a":{"type":["string","null"]},"b":{"type":["string","null","integer"]}}}`)
	if breaks := Check(old, neu); len(breaks) != 0 {
		t.Fatalf("adding a type to a union must be compatible, got %v", breaks)
	}
}

func TestCheck_UnionTypeRemovalIsBreaking(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"a":{"type":["string","null"]},"b":{"type":["string","integer"]}}}`)
	neu := parse(t, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":["integer","boolean"]}}}`)
	breaks := Check(old, neu)
	if len(breaks) != 2 {
		t.Fatalf("removing a type from a union must be breaking for both properties, got %v", breaks)
	}
	want := map[string]bool{
		`a: type changed ["string","null"] → "string"`:                 true,
		`b: type changed ["string","integer"] → ["integer","boolean"]`: true,
	}
	for _, b := range breaks {
		if !want[b] {
			t.Fatalf("unexpected break %q (all: %v)", b, breaks)
		}
	}
}

func TestCheck_UnionIsOrderInsensitiveAndDescends(t *testing.T) {
	old := parse(t, `{"type":["object","null"],"properties":{"a":{"type":"string"}}}`)
	same := parse(t, `{"type":["null","object"],"properties":{"a":{"type":"string"}}}`)
	if breaks := Check(old, same); len(breaks) != 0 {
		t.Fatalf("reordering a union must be compatible, got %v", breaks)
	}
	nested := parse(t, `{"type":["null","object"],"properties":{"a":{"type":"integer"}}}`)
	if breaks := Check(old, nested); len(breaks) != 1 || breaks[0] != `a: type changed "string" → "integer"` {
		t.Fatalf("a nested retype under a union must still be reported, got %v", breaks)
	}
}

func TestCheck_MaxLengthTightenedIsBreaking(t *testing.T) {
	old := parse(t, `{"type":"object","properties":{"s":{"type":"string","maxLength":10}}}`)
	if breaks := Check(old, parse(t, `{"type":"object","properties":{"s":{"type":"string","maxLength":5}}}`)); len(breaks) != 1 {
		t.Fatalf("lowering maxLength must be breaking, got %v", breaks)
	}
	if breaks := Check(old, parse(t, `{"type":"object","properties":{"s":{"type":"string","maxLength":20}}}`)); len(breaks) != 0 {
		t.Fatalf("raising maxLength must be compatible, got %v", breaks)
	}
	free := parse(t, `{"type":"object","properties":{"s":{"type":"string"}}}`)
	if breaks := Check(free, old); len(breaks) != 1 {
		t.Fatalf("adding maxLength where none existed must be breaking, got %v", breaks)
	}
}

func TestCheck_UnknownTypeNamesConstrainNothing(t *testing.T) {
	// "strng" is not a JSON Schema type, so validation ignores it: both schemas accept exactly
	// the strings, and the lint must agree with the validator.
	old := parse(t, `{"type":"object","properties":{"a":{"type":["string","strng"]}}}`)
	neu := parse(t, `{"type":"object","properties":{"a":{"type":"string"}}}`)
	if breaks := Check(old, neu); len(breaks) != 0 {
		t.Fatalf("an unknown type name must not count as an accepted type, got %v", breaks)
	}
}
