package restapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/babelqueue/babelqueue-registry/internal/compat"
	"github.com/babelqueue/babelqueue-registry/internal/schema"
)

// versionView is the Confluent shape for GET /subjects/{subject}/versions/{version}.
type versionView struct {
	Subject string `json:"subject"`
	Version int    `json:"version"`
	ID      int    `json:"id"`
	Schema  string `json:"schema"` // the JSON Schema document, as a string
}

// schemaView is the Confluent shape for GET /schemas/ids/{id}.
type schemaView struct {
	Schema string `json:"schema"`
}

// configView is the Confluent shape for GET /config and GET /config/{subject}.
type configView struct {
	CompatibilityLevel string `json:"compatibilityLevel"`
}

// compatibilityView is the Confluent shape for POST /compatibility/...
type compatibilityView struct {
	IsCompatible bool `json:"is_compatible"`
}

// registerRequest is the Confluent body for compatibility checks (and registration): the
// candidate schema arrives as a JSON-encoded string in the "schema" field.
type registerRequest struct {
	Schema string `json:"schema"`
}

// handleListSubjects: GET /subjects → ["urn:...","urn:..."].
func (s *Server) handleListSubjects(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.subjectNames())
}

// handleListVersions: GET /subjects/{subject}/versions → [1]. Every subject has exactly one
// version because the file registry stores one schema per URN.
func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.subjects[r.PathValue("subject")]; !ok {
		writeError(w, http.StatusNotFound, errCodeSubjectNotFound, "Subject not found.")
		return
	}
	writeJSON(w, http.StatusOK, []int{onlyVersion})
}

// handleGetVersion: GET /subjects/{subject}/versions/{version} and /latest →
// {subject, version, id, schema}. Only version 1 (or "latest") exists.
func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subjects[r.PathValue("subject")]
	if !ok {
		writeError(w, http.StatusNotFound, errCodeSubjectNotFound, "Subject not found.")
		return
	}
	version, ok := parseVersion(r.PathValue("version"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, errCodeInvalidVersion,
			"The specified version is not a valid version id. Allowed values are between [1, 2^31-1] and the string \"latest\"")
		return
	}
	if version != onlyVersion {
		writeError(w, http.StatusNotFound, errCodeVersionNotFound, "Version not found.")
		return
	}
	if sub.schema == "" { // schema file was unreadable at load time.
		writeError(w, http.StatusInternalServerError, errCodeBackendError,
			"Error while retrieving schema from the backend datastore.")
		return
	}
	writeJSON(w, http.StatusOK, versionView{
		Subject: sub.urn,
		Version: onlyVersion,
		ID:      sub.id,
		Schema:  sub.schema,
	})
}

// handleGetByID: GET /schemas/ids/{id} → {schema}. Ids are the stable, 1-based ids New assigned.
func (s *Server) handleGetByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errCodeSchemaNotFound, "Schema not found")
		return
	}
	sub, ok := s.byID[id]
	if !ok {
		writeError(w, http.StatusNotFound, errCodeSchemaNotFound, "Schema not found")
		return
	}
	if sub.schema == "" {
		writeError(w, http.StatusInternalServerError, errCodeBackendError,
			"Error while retrieving schema from the backend datastore.")
		return
	}
	writeJSON(w, http.StatusOK, schemaView{Schema: sub.schema})
}

// handleCompatibility: POST /compatibility/subjects/{subject}/versions/{version} →
// {is_compatible}. The posted candidate schema is checked, with the EXISTING compat engine,
// against the schema currently registered for the subject. Compatibility direction is BACKWARD
// (Confluent's default): the candidate is treated as the NEW schema and must still accept data
// valid under the registered (OLD) schema — i.e. compat.Check(registered, candidate) is empty.
func (s *Server) handleCompatibility(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subjects[r.PathValue("subject")]
	if !ok {
		writeError(w, http.StatusNotFound, errCodeSubjectNotFound, "Subject not found.")
		return
	}
	version, ok := parseVersion(r.PathValue("version"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, errCodeInvalidVersion,
			"The specified version is not a valid version id. Allowed values are between [1, 2^31-1] and the string \"latest\"")
		return
	}
	if version != onlyVersion {
		writeError(w, http.StatusNotFound, errCodeVersionNotFound, "Version not found.")
		return
	}
	if sub.schema == "" {
		writeError(w, http.StatusInternalServerError, errCodeBackendError,
			"Error while retrieving schema from the backend datastore.")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, errCodeInvalidVersion, "Could not read request body.")
		return
	}
	var req registerRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Schema == "" {
		writeError(w, http.StatusUnprocessableEntity, errCodeInvalidVersion,
			"Invalid request: expected a JSON body with a \"schema\" string.")
		return
	}

	registered, err := schema.Parse([]byte(sub.schema))
	if err != nil {
		writeError(w, http.StatusInternalServerError, errCodeBackendError,
			"Error while parsing the registered schema.")
		return
	}
	candidate, err := schema.Parse([]byte(req.Schema))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, errCodeInvalidVersion,
			"Invalid schema in \"schema\": "+err.Error())
		return
	}

	breaks := compat.Check(registered, candidate)
	writeJSON(w, http.StatusOK, compatibilityView{IsCompatible: len(breaks) == 0})
}

// handleConfig: GET /config → {compatibilityLevel} (the registry-wide level).
func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, configView{CompatibilityLevel: s.level})
}

// handleSubjectConfig: GET /config/{subject} → {compatibilityLevel}. The file registry has no
// per-subject overrides, so a known subject reports the registry-wide level; an unknown one 404s.
func (s *Server) handleSubjectConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.subjects[r.PathValue("subject")]; !ok {
		writeError(w, http.StatusNotFound, errCodeSubjectNotFound, "Subject not found.")
		return
	}
	writeJSON(w, http.StatusOK, configView{CompatibilityLevel: s.level})
}

// handleNotFound is the fallthrough for any unmatched route: a Confluent-style 404 body rather
// than ServeMux's plain-text "404 page not found".
func (s *Server) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, errCodeSchemaNotFound, "Not found.")
}

// parseVersion accepts a positive integer version or the literal "latest" (which resolves to the
// only version, 1). It returns the resolved version and whether the input was well-formed.
func parseVersion(raw string) (int, bool) {
	if raw == "latest" {
		return onlyVersion, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}
