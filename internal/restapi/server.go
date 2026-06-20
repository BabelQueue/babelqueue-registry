// Package restapi exposes a read-mostly, Confluent Schema Registry-compatible REST surface
// over the EXISTING file-based BabelQueue registry. It does NOT replace the git-native file
// store — it serves the same git-tracked per-URN `data` schemas through the wire shapes a
// Confluent client expects, so existing Schema-Registry tooling can introspect a BabelQueue
// registry without a broker, a database, or PHP `serialize()`.
//
// Mapping (documented in the README): each registry URN is one Confluent "subject", named by
// the URN verbatim (e.g. `urn:babel:orders:created`). Because the file registry holds exactly
// one schema per URN, every subject has exactly one version — version 1 — and "latest" resolves
// to it. Schema ids are assigned deterministically (1-based, in lexical URN order) at load time,
// so an id is stable for a given registry and `GET /schemas/ids/{id}` is well-defined.
//
// Registration (`POST /subjects/{subject}/versions`) is intentionally OUT OF SCOPE: the registry
// is git-managed, so schemas are added by committing files, not by writing over REST. The server
// never mutates the file store.
package restapi

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/babelqueue/babelqueue-registry/internal/registry"
)

// contentType is Confluent Schema Registry's versioned media type. Responses set it so a
// Confluent-aware client recognises the surface; the body is still plain JSON.
const contentType = "application/vnd.schemaregistry.v1+json"

// onlyVersion is the single version every subject has: the file registry stores one schema per
// URN, so there is exactly one version, numbered 1 (Confluent versions are 1-based).
const onlyVersion = 1

// Confluent error codes used by this surface (a subset of the documented catalogue). Confluent
// returns these in the body as {"error_code": int, "message": string}.
const (
	errCodeSubjectNotFound = 40401 // Subject not found.
	errCodeVersionNotFound = 40402 // Version not found.
	errCodeSchemaNotFound  = 40403 // Schema not found (by id).
	errCodeInvalidVersion  = 42202 // The version is not a valid version id or "latest".
	errCodeBackendError    = 50001 // Error in the backend datastore (schema file unreadable).
)

// subject is a registry URN resolved to its Confluent view: a stable id and the raw JSON Schema
// string for the URN's `data` block.
type subject struct {
	urn    string
	id     int
	schema string
}

// Server is a Confluent-compatible HTTP handler backed by a loaded file registry. It is
// read-mostly and never mutates the underlying store. Build it with New and mount it with Handler.
type Server struct {
	reg      *registry.Registry
	level    string             // the single, registry-wide compatibility level reported by /config
	subjects map[string]subject // subject name (== URN) → resolved subject
	byID     map[int]subject    // schema id → subject (for GET /schemas/ids/{id})
}

// New loads every URN's raw schema up front (so request handling does no further file IO for the
// happy path) and assigns deterministic schema ids in lexical URN order. A URN whose schema file
// cannot be read is recorded with an empty schema and surfaced as a backend error on access,
// rather than failing the whole server — the registry is git-managed and may be mid-edit. level
// is the compatibility level the surface advertises (e.g. "BACKWARD"); pass "" for the default.
func New(reg *registry.Registry, level string) *Server {
	if level == "" {
		level = "BACKWARD"
	}
	s := &Server{
		reg:      reg,
		level:    level,
		subjects: map[string]subject{},
		byID:     map[int]subject{},
	}
	for i, urn := range reg.URNs() { // URNs() is already lexically sorted → stable ids.
		sub := subject{urn: urn, id: i + 1}
		if raw, ok, err := reg.RawSchema(urn); err == nil && ok {
			sub.schema = string(raw)
		}
		s.subjects[urn] = sub
		s.byID[sub.id] = sub
	}
	return s
}

// Handler returns the HTTP handler implementing the supported Confluent endpoints. The method +
// path patterns use the Go 1.22 ServeMux routing syntax; unmatched routes fall through to a
// Confluent-style 404 body.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /subjects", s.handleListSubjects)
	mux.HandleFunc("GET /subjects/{subject}/versions", s.handleListVersions)
	mux.HandleFunc("GET /subjects/{subject}/versions/{version}", s.handleGetVersion)
	mux.HandleFunc("GET /schemas/ids/{id}", s.handleGetByID)
	mux.HandleFunc("POST /compatibility/subjects/{subject}/versions/{version}", s.handleCompatibility)
	mux.HandleFunc("GET /config", s.handleConfig)
	mux.HandleFunc("GET /config/{subject}", s.handleSubjectConfig)
	// Any other path: Confluent-style 404 (rather than ServeMux's plain-text default).
	mux.HandleFunc("/", s.handleNotFound)
	return mux
}

// subjectNames returns the subject names (URNs) in lexical order.
func (s *Server) subjectNames() []string {
	out := make([]string, 0, len(s.subjects))
	for name := range s.subjects {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// writeJSON marshals v with the Confluent content type and the given status. A marshalling
// failure (which should not happen for our own value types) degrades to a backend error.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errCodeBackendError, "failed to encode response")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// apiError is Confluent's error body shape.
type apiError struct {
	ErrorCode int    `json:"error_code"`
	Message   string `json:"message"`
}

// writeError emits a Confluent-style error body with the documented content type.
func writeError(w http.ResponseWriter, httpStatus, errorCode int, message string) {
	writeJSON(w, httpStatus, apiError{ErrorCode: errorCode, Message: message})
}
