// Package registry loads a file-based, broker-free per-URN schema registry: a
// `registry.json` manifest mapping each message URN to a draft-07 JSON Schema file for
// its `data` block. This realizes urn-naming.md §6's recommended checked-in registry as
// real, git-tracked artifacts — no Kafka, no service, no circular dependency.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/babelqueue/babelqueue-registry/internal/schema"
)

// Entry is one URN's governance record.
type Entry struct {
	URN    string `json:"urn"`
	Schema string `json:"schema"` // path to the data JSON Schema, relative to the manifest dir
	Owner  string `json:"owner,omitempty"`
	Status string `json:"status,omitempty"`
}

type manifest struct {
	Schemas []Entry `json:"schemas"`
}

// Registry is a loaded manifest plus the directory its schema paths resolve against.
type Registry struct {
	dir     string
	entries map[string]Entry
}

// Load reads and validates the manifest at manifestPath. It checks that every entry has a
// URN and schema path and that no URN is declared twice; it does not yet read the schema
// files (Schema does that lazily).
func Load(manifestPath string) (*Registry, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("registry: read %s: %w", manifestPath, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("registry: parse %s: %w", manifestPath, err)
	}

	r := &Registry{dir: filepath.Dir(manifestPath), entries: make(map[string]Entry, len(m.Schemas))}
	for i, e := range m.Schemas {
		if e.URN == "" {
			return nil, fmt.Errorf("registry: entry %d has an empty urn", i)
		}
		if e.Schema == "" {
			return nil, fmt.Errorf("registry: entry %q has an empty schema path", e.URN)
		}
		if _, dup := r.entries[e.URN]; dup {
			return nil, fmt.Errorf("registry: urn %q is declared more than once", e.URN)
		}
		r.entries[e.URN] = e
	}
	return r, nil
}

// URNs returns the declared URNs in lexical order.
func (r *Registry) URNs() []string {
	out := make([]string, 0, len(r.entries))
	for urn := range r.entries {
		out = append(out, urn)
	}
	sort.Strings(out)
	return out
}

// Has reports whether urn is declared.
func (r *Registry) Has(urn string) bool {
	_, ok := r.entries[urn]
	return ok
}

// RawSchema returns the unparsed JSON Schema bytes registered for urn (full fidelity,
// for export). The second return is false when the URN is not declared.
func (r *Registry) RawSchema(urn string) ([]byte, bool, error) {
	e, ok := r.entries[urn]
	if !ok {
		return nil, false, nil
	}
	path := e.Schema
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.dir, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, true, fmt.Errorf("registry: read schema for %q (%s): %w", urn, e.Schema, err)
	}
	return raw, true, nil
}

// Schema reads and parses (into the subset model) the data schema registered for urn. The
// second return is false when the URN is not declared.
func (r *Registry) Schema(urn string) (*schema.Schema, bool, error) {
	raw, ok, err := r.RawSchema(urn)
	if err != nil || !ok {
		return nil, ok, err
	}
	s, err := schema.Parse(raw)
	if err != nil {
		return nil, true, fmt.Errorf("registry: %q: %w", urn, err)
	}
	return s, true, nil
}
