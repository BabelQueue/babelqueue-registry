// Package gdpr makes a BabelQueue schema registry's PII sensitivity declared, auditable, and
// maskable at the GOVERNANCE layer — without touching the wire envelope or any SDK. It reads the
// "x-gdpr-sensitive" extension keyword (parsed by internal/schema) and offers three things:
//
//   - Inventory — for each URN, the sensitive field paths plus a coverage summary, for review/docs.
//   - Audit — a CI gate that flags property names that LOOK like PII (email, ssn, phone, tckn, …)
//     but are NOT marked x-gdpr-sensitive, so un-annotated PII fails the build.
//   - Mask — given a message and its URN schema, a copy with the sensitive fields masked, safe for
//     logging or fixtures.
//
// The registry only DECLARES and AUDITS sensitivity. Field-level runtime encryption/tokenisation
// is an SDK concern and out of scope here: an SDK consumes this same x-gdpr-sensitive annotation
// (e.g. via the AsyncAPI catalog or by reading the schema) to decide which fields to encrypt on
// publish and decrypt on consume. Mask is the registry-side, reversible-by-nobody equivalent used
// for safe logging — not a crypto primitive.
package gdpr

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/babelqueue/babelqueue-registry/internal/registry"
	"github.com/babelqueue/babelqueue-registry/internal/schema"
)

// DefaultPIIPattern matches property names that conventionally hold personal data, so the audit can
// flag any such field that is NOT marked x-gdpr-sensitive. It is deliberately broad and
// case-insensitive; teams override it via Audit's pattern argument. "tckn" is the Turkish national
// id (Türkiye Cumhuriyeti Kimlik Numarası).
const DefaultPIIPattern = `(?i)(email|e_mail|mail|ssn|phone|msisdn|tckn|national_id|passport|iban|credit_card|card_number|cvv|address|birth|dob)`

// URNInventory is one URN's sensitivity record: its sensitive field paths (sorted).
type URNInventory struct {
	URN   string
	Paths []schema.SensitivePath
}

// Inventory is the registry-wide audit/inventory: per-URN sensitive paths plus totals.
type Inventory struct {
	URNs        []URNInventory
	TotalURNs   int // number of URNs in the registry
	TotalFields int // total marked sensitive fields across all URNs
}

// BuildInventory loads every URN's schema and collects its x-gdpr-sensitive paths. A URN whose
// schema cannot be read or parsed returns an error (the registry should already pass `check`).
func BuildInventory(r *registry.Registry) (*Inventory, error) {
	inv := &Inventory{}
	for _, urn := range r.URNs() {
		inv.TotalURNs++
		s, ok, err := r.Schema(urn)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		paths := s.SensitivePaths()
		inv.TotalFields += len(paths)
		inv.URNs = append(inv.URNs, URNInventory{URN: urn, Paths: paths})
	}
	return inv, nil
}

// Finding is one un-annotated PII property the audit caught: a property whose NAME matches the PII
// pattern but which is NOT marked x-gdpr-sensitive.
type Finding struct {
	URN  string
	Path string // dotted path to the offending property
}

// AuditResult is the outcome of a --require audit run.
type AuditResult struct {
	Findings    []Finding // un-annotated PII fields (sorted by URN then path); empty means the audit passed
	CheckedURNs int
}

// OK reports whether the audit found no un-annotated PII (i.e. the --require gate passes).
func (a *AuditResult) OK() bool { return len(a.Findings) == 0 }

// Audit walks every URN's schema and reports property names matching pattern that are NOT marked
// x-gdpr-sensitive — i.e. likely PII that someone forgot to annotate. Pass "" to use
// DefaultPIIPattern. It returns an error only for an unusable registry or an invalid pattern.
func Audit(r *registry.Registry, pattern string) (*AuditResult, error) {
	if pattern == "" {
		pattern = DefaultPIIPattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("gdpr: invalid PII name pattern: %w", err)
	}

	res := &AuditResult{}
	for _, urn := range r.URNs() {
		s, ok, err := r.Schema(urn)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		res.CheckedURNs++
		auditNode("", s, re, urn, res)
	}
	sort.Slice(res.Findings, func(i, j int) bool {
		if res.Findings[i].URN != res.Findings[j].URN {
			return res.Findings[i].URN < res.Findings[j].URN
		}
		return res.Findings[i].Path < res.Findings[j].Path
	})
	return res, nil
}

// auditNode descends the schema; for each PII-NAMED, value-bearing property that is NOT marked
// sensitive, it records a finding. It matches on the property NAME (the leaf key), so a nested
// `customer.email` is caught by the "email" name. Object/array containers are not flagged
// themselves — they carry no scalar PII value, and their sensitive content is annotated on the
// leaves (so the leaves, not the container, are the unit of annotation). An unmarked PII-named
// container therefore still fails the gate via its leaves, not via the container name.
func auditNode(path string, s *schema.Schema, re *regexp.Regexp, urn string, res *AuditResult) {
	if s == nil {
		return
	}
	for name, sub := range s.Properties {
		childPath := joinPath(path, name)
		if re.MatchString(name) && !marked(sub) && !isContainer(sub) {
			res.Findings = append(res.Findings, Finding{URN: urn, Path: childPath})
		}
		auditNode(childPath, sub, re, urn, res)
	}
	if s.Items != nil {
		auditNode(path+"[]", s.Items, re, urn, res)
	}
}

func marked(s *schema.Schema) bool { return s != nil && s.GDPRSensitive }

// isContainer reports whether the property holds a structured value (object/array) rather than a
// scalar; such a node cannot itself carry a PII value, so the audit looks at its leaves instead.
func isContainer(s *schema.Schema) bool {
	return s != nil && (s.Type == "object" || s.Type == "array" || len(s.Properties) > 0 || s.Items != nil)
}

// Mask returns a deep copy of value with every field marked x-gdpr-sensitive in s replaced by a
// masked placeholder, leaving all other fields intact. It is non-destructive: the input value is
// not modified. It is for safe logging/fixtures, NOT encryption — masked values are unrecoverable.
//
// Masking is leaf-aware: a sensitive string is partially masked (first character kept, the rest
// "***", e.g. "alice@x.com" → "a***"), so a masked value is still distinguishable in a log while
// the PII content is gone; any other sensitive value (number, bool, object, array) becomes "***".
func Mask(value any, s *schema.Schema) any {
	return maskNode(value, s)
}

const maskPlaceholder = "***"

func maskNode(value any, s *schema.Schema) any {
	if s != nil && s.GDPRSensitive {
		return maskValue(value)
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, val := range v {
			if s != nil {
				if sub, ok := s.Properties[key]; ok {
					out[key] = maskNode(val, sub)
					continue
				}
			}
			out[key] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		var items *schema.Schema
		if s != nil {
			items = s.Items
		}
		for i, val := range v {
			out[i] = maskNode(val, items)
		}
		return out
	default:
		return deepCopy(value)
	}
}

// maskValue masks a single sensitive leaf: a string keeps its first rune then "***"; anything else
// (number, bool, null, or a whole object/array marked sensitive) collapses to "***".
func maskValue(value any) any {
	if str, ok := value.(string); ok && str != "" {
		r := []rune(str)
		return string(r[:1]) + maskPlaceholder
	}
	return maskPlaceholder
}

// deepCopy clones the decoded-JSON value so the masked result never aliases the caller's input.
func deepCopy(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = deepCopy(val)
		}
		return out
	default:
		return v
	}
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
