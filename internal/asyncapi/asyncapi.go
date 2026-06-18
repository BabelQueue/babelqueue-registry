// Package asyncapi turns a BabelQueue schema registry into an AsyncAPI 3.0 document:
// each registered URN becomes a channel whose message payload is that URN's `data` JSON
// Schema. The output is a discoverable, tool-agnostic event catalog — generated from the
// same git-tracked schemas the validator uses, with no broker or service involved.
package asyncapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/babelqueue/babelqueue-registry/internal/registry"
)

// Build constructs an AsyncAPI 3.0 document (as a generic map ready to marshal) from the
// registry. Each URN's raw `data` schema is embedded verbatim as the message payload, so
// no JSON Schema fidelity is lost.
func Build(title, version string, r *registry.Registry) (map[string]any, error) {
	channels := map[string]any{}
	messages := map[string]any{}

	for _, urn := range r.URNs() {
		raw, ok, err := r.RawSchema(urn)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var payload any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("asyncapi: %q: invalid schema JSON: %w", urn, err)
		}

		id := componentID(urn)
		messages[id] = map[string]any{
			"name":    urn,
			"payload": payload,
		}
		channels[urn] = map[string]any{
			"address": urn,
			"messages": map[string]any{
				id: map[string]any{"$ref": "#/components/messages/" + id},
			},
		}
	}

	return map[string]any{
		"asyncapi": "3.0.0",
		"info": map[string]any{
			"title":   title,
			"version": version,
		},
		"channels": channels,
		"components": map[string]any{
			"messages": messages,
		},
	}, nil
}

// componentID maps a URN to an AsyncAPI-safe component key: every character outside
// [A-Za-z0-9_] becomes "_" (so `urn:babel:orders:created` → `urn_babel_orders_created`).
func componentID(urn string) string {
	var b strings.Builder
	for _, r := range urn {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
