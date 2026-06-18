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
	if breaks := Check(old, neu); len(breaks) == 0 {
		t.Fatal("removing a property while additionalProperties is false must be breaking")
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
