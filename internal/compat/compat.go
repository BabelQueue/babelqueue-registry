// Package compat reports backward-incompatible differences between two versions of a
// message's `data` schema — the ways data valid under the OLD schema could be rejected by
// the NEW one. It encodes versioning-policy.md §3: additive optional fields are safe; a
// removal / rename / retype, or making an optional field required, is breaking and means a
// new URN must be minted (`…:created.v2`) rather than mutating the existing one.
package compat

import (
	"fmt"
	"sort"

	"github.com/babelqueue/babelqueue-registry/internal/schema"
)

// Check returns the backward-incompatible changes from old → new. An empty result means
// the change is backward-compatible (a consumer on `new` still accepts a producer on
// `old`, honouring the golden rule "consumers upgrade before producers").
func Check(old, new *schema.Schema) []string {
	var breaks []string
	compare("", old, new, &breaks)
	sort.Strings(breaks)
	return breaks
}

func compare(path string, old, new *schema.Schema, breaks *[]string) {
	if old == nil || new == nil {
		return
	}
	at := func(msg string) string {
		if path == "" {
			return msg
		}
		return path + ": " + msg
	}

	// A retype invalidates everything beneath it; report and stop descending.
	if old.Type != "" && new.Type != "" && old.Type != new.Type {
		*breaks = append(*breaks, at(fmt.Sprintf("type changed %q → %q", old.Type, new.Type)))
		return
	}

	// Narrowing a previously-unconstrained value, or dropping allowed enum values, rejects
	// data the old schema accepted.
	if old.Enum == nil && new.Enum != nil {
		*breaks = append(*breaks, at("enum constraint added where any value was allowed"))
	} else if old.Enum != nil && new.Enum != nil {
		allowed := valueSet(new.Enum)
		for _, v := range old.Enum {
			if _, ok := allowed[fmt.Sprintf("%v", v)]; !ok {
				*breaks = append(*breaks, at(fmt.Sprintf("enum value %v removed", v)))
			}
		}
	}
	if tightenedInt(old.MinLength, new.MinLength) {
		*breaks = append(*breaks, at("minLength tightened"))
	}
	if tightenedFloat(old.Minimum, new.Minimum) {
		*breaks = append(*breaks, at("minimum tightened"))
	}

	// Object: newly-required fields and a closed door reject old data.
	oldReq := toSet(old.Required)
	for _, f := range new.Required {
		if _, was := oldReq[f]; !was {
			*breaks = append(*breaks, at(fmt.Sprintf("property %q is now required", f)))
		}
	}
	if isOpen(old.AdditionalProperties) && isClosed(new.AdditionalProperties) {
		*breaks = append(*breaks, at("additionalProperties tightened to false"))
	}
	for name, oldP := range old.Properties {
		newP, ok := new.Properties[name]
		if !ok {
			if isClosed(new.AdditionalProperties) {
				*breaks = append(*breaks, at(fmt.Sprintf("property %q removed while additionalProperties is false", name)))
			}
			continue
		}
		compare(join(path, name), oldP, newP, breaks)
	}

	// Array item schema.
	if old.Items != nil && new.Items != nil {
		compare(path+"[]", old.Items, new.Items, breaks)
	}
}

func isOpen(ap *bool) bool   { return ap == nil || *ap }
func isClosed(ap *bool) bool { return ap != nil && !*ap }

func toSet(keys []string) map[string]struct{} {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}

func valueSet(values []any) map[string]struct{} {
	m := make(map[string]struct{}, len(values))
	for _, v := range values {
		m[fmt.Sprintf("%v", v)] = struct{}{}
	}
	return m
}

func tightenedInt(old, new *int) bool {
	if new == nil {
		return false
	}
	return old == nil || *new > *old
}

func tightenedFloat(old, new *float64) bool {
	if new == nil {
		return false
	}
	return old == nil || *new > *old
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
