// Package compat reports backward-incompatible differences between two versions of a
// message's `data` schema — the ways data valid under the OLD schema could be rejected by
// the NEW one. It encodes versioning-policy.md §3: additive optional fields are safe; a
// removal / rename / retype, or making an optional field required, is breaking and means a
// new URN must be minted (`…:created.v2`) rather than mutating the existing one.
package compat

import (
	"fmt"
	"sort"
	"strings"

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

	// A narrowed or changed type invalidates everything beneath it; report and stop
	// descending. `type` may be a union (an array): removing a type from the accepted set is
	// breaking, adding one is not (data valid under the old set stays valid). A single type
	// that differs from the old one is the degenerate case — the old type is no longer accepted.
	if oldTypes, newTypes := typeSet(old), typeSet(new); len(oldTypes) > 0 && len(newTypes) > 0 {
		for _, t := range oldTypes {
			if !contains(newTypes, t) {
				*breaks = append(*breaks, at(fmt.Sprintf("type changed %s → %s", formatTypes(oldTypes), formatTypes(newTypes))))
				return
			}
		}
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
	if tightenedMax(old.MaxLength, new.MaxLength) {
		*breaks = append(*breaks, at("maxLength tightened"))
	}
	if tightenedFloat(old.Minimum, new.Minimum) {
		*breaks = append(*breaks, at("minimum tightened"))
	}

	// Object: newly-required fields, removed fields and a closed door are breaking.
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
			// A removal is breaking whatever additionalProperties says (versioning-policy.md
			// §3): an open schema would still accept the old field, but its type and meaning
			// are no longer governed, so producers on `old` silently lose the contract.
			*breaks = append(*breaks, fmt.Sprintf("%s: property removed", join(path, name)))
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

// tightenedMax reports whether an upper bound became stricter: newly added, or lowered.
func tightenedMax(old, new *int) bool {
	if new == nil {
		return false
	}
	return old == nil || *new < *old
}

// typeSet returns the types a schema node accepts — the same set validation enforces (the
// single "type", or every entry of a "type" union, restricted to the seven JSON Schema types).
// An empty result means the node does not constrain the type.
func typeSet(s *schema.Schema) []string {
	return s.KnownTypes()
}

func contains(set []string, t string) bool {
	for _, v := range set {
		if v == t {
			return true
		}
	}
	return false
}

// formatTypes renders a single type as "string" and a union as ["string","null"].
func formatTypes(types []string) string {
	if len(types) == 1 {
		return fmt.Sprintf("%q", types[0])
	}
	quoted := make([]string, len(types))
	for i, t := range types {
		quoted[i] = fmt.Sprintf("%q", t)
	}
	return "[" + strings.Join(quoted, ",") + "]"
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
