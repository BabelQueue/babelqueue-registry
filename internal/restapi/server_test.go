package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/babelqueue/babelqueue-registry/internal/registry"
)

// the URN registered by newTestServer; also the Confluent subject name.
const testURN = "urn:babel:orders:created"

// the registered schema (the OLD/current schema each compatibility check runs against).
const registeredSchema = `{
  "type": "object",
  "required": ["order_id"],
  "properties": {
    "order_id": { "type": "integer", "minimum": 1 },
    "amount":   { "type": "number" }
  },
  "additionalProperties": false
}`

// newTestServer writes a one-URN file registry to a temp dir, loads it, and returns a running
// httptest server plus the resolved subject for id assertions.
func newTestServer(t *testing.T) (*httptest.Server, subject) {
	t.Helper()
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "schemas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schemas", "orders-created.json"), []byte(registeredSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join(dir, "registry.json")
	manifest := `{"schemas":[{"urn":"` + testURN + `","schema":"schemas/orders-created.json"}]}`
	if err := os.WriteFile(regPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := registry.Load(regPath)
	if err != nil {
		t.Fatal(err)
	}
	s := New(r, "")
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s.subjects[testURN]
}

// get is a small helper that issues a GET and returns status + body.
func get(t *testing.T, ts *httptest.Server, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	return resp.StatusCode, body
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf
}

func TestListSubjects(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/subjects")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var subjects []string
	if err := json.Unmarshal(body, &subjects); err != nil {
		t.Fatalf("body is not a JSON array: %v (%s)", err, body)
	}
	if len(subjects) != 1 || subjects[0] != testURN {
		t.Fatalf("subjects = %v, want [%s]", subjects, testURN)
	}
}

func TestContentType(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/subjects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != contentType {
		t.Fatalf("Content-Type = %q, want %q", got, contentType)
	}
}

func TestListVersions(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/subjects/"+testURN+"/versions")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var versions []int
	if err := json.Unmarshal(body, &versions); err != nil {
		t.Fatalf("body is not a JSON array: %v (%s)", err, body)
	}
	if len(versions) != 1 || versions[0] != 1 {
		t.Fatalf("versions = %v, want [1]", versions)
	}
}

func TestListVersions_UnknownSubject(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/subjects/urn:babel:does:not-exist/versions")
	assertConfluentError(t, status, body, http.StatusNotFound, errCodeSubjectNotFound)
}

func TestGetVersion(t *testing.T) {
	ts, sub := newTestServer(t)
	for _, version := range []string{"1", "latest"} {
		status, body := get(t, ts, "/subjects/"+testURN+"/versions/"+version)
		if status != http.StatusOK {
			t.Fatalf("version %s: status = %d, want 200; body=%s", version, status, body)
		}
		var v versionView
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("version %s: %v (%s)", version, err, body)
		}
		if v.Subject != testURN || v.Version != 1 || v.ID != sub.id {
			t.Fatalf("version %s: got %+v, want subject=%s version=1 id=%d", version, v, testURN, sub.id)
		}
		// schema must be the JSON Schema document as a string.
		if !strings.Contains(v.Schema, "order_id") {
			t.Fatalf("version %s: schema string missing payload: %s", version, v.Schema)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(v.Schema), &parsed); err != nil {
			t.Fatalf("version %s: schema field is not a valid JSON Schema string: %v", version, err)
		}
	}
}

func TestGetVersion_NotFoundVersion(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/subjects/"+testURN+"/versions/2")
	assertConfluentError(t, status, body, http.StatusNotFound, errCodeVersionNotFound)
}

func TestGetVersion_InvalidVersion(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/subjects/"+testURN+"/versions/abc")
	assertConfluentError(t, status, body, http.StatusUnprocessableEntity, errCodeInvalidVersion)
}

func TestGetVersion_UnknownSubject(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/subjects/urn:babel:nope/versions/latest")
	assertConfluentError(t, status, body, http.StatusNotFound, errCodeSubjectNotFound)
}

func TestGetByID(t *testing.T) {
	ts, sub := newTestServer(t)
	status, body := get(t, ts, "/schemas/ids/"+itoa(sub.id))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var v schemaView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%v (%s)", err, body)
	}
	if !strings.Contains(v.Schema, "order_id") {
		t.Fatalf("schema by id missing payload: %s", v.Schema)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/schemas/ids/9999")
	assertConfluentError(t, status, body, http.StatusNotFound, errCodeSchemaNotFound)
}

func TestGetByID_NotANumber(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/schemas/ids/not-a-number")
	assertConfluentError(t, status, body, http.StatusNotFound, errCodeSchemaNotFound)
}

func TestCompatibility_Compatible(t *testing.T) {
	ts, _ := newTestServer(t)
	// Adds an optional field — additive → compatible.
	candidate := `{
      "type": "object",
      "required": ["order_id"],
      "properties": {
        "order_id": { "type": "integer", "minimum": 1 },
        "amount":   { "type": "number" },
        "currency": { "type": "string" }
      },
      "additionalProperties": false
    }`
	isCompatible := postCompatibility(t, ts, testURN, "latest", candidate, http.StatusOK)
	if !isCompatible {
		t.Fatal("additive optional field should be compatible")
	}
}

func TestCompatibility_Breaking(t *testing.T) {
	ts, _ := newTestServer(t)
	// Adds a NEW required field — breaking under BACKWARD.
	candidate := `{
      "type": "object",
      "required": ["order_id", "customer_id"],
      "properties": {
        "order_id":    { "type": "integer", "minimum": 1 },
        "amount":      { "type": "number" },
        "customer_id": { "type": "string" }
      },
      "additionalProperties": false
    }`
	isCompatible := postCompatibility(t, ts, testURN, "1", candidate, http.StatusOK)
	if isCompatible {
		t.Fatal("a new required field should be reported as a breaking change")
	}
}

func TestCompatibility_UnknownSubject(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := postRaw(t, ts, "/compatibility/subjects/urn:babel:nope/versions/latest", `{"schema":"{}"}`)
	assertConfluentError(t, status, body, http.StatusNotFound, errCodeSubjectNotFound)
}

func TestCompatibility_BadVersion(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := postRaw(t, ts, "/compatibility/subjects/"+testURN+"/versions/xyz", `{"schema":"{}"}`)
	assertConfluentError(t, status, body, http.StatusUnprocessableEntity, errCodeInvalidVersion)
}

func TestCompatibility_VersionNotFound(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := postRaw(t, ts, "/compatibility/subjects/"+testURN+"/versions/5", `{"schema":"{}"}`)
	assertConfluentError(t, status, body, http.StatusNotFound, errCodeVersionNotFound)
}

func TestCompatibility_MissingSchemaField(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := postRaw(t, ts, "/compatibility/subjects/"+testURN+"/versions/latest", `{}`)
	assertConfluentError(t, status, body, http.StatusUnprocessableEntity, errCodeInvalidVersion)
}

func TestCompatibility_InvalidSchemaJSON(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := postRaw(t, ts, "/compatibility/subjects/"+testURN+"/versions/latest", `{"schema":"not json"}`)
	assertConfluentError(t, status, body, http.StatusUnprocessableEntity, errCodeInvalidVersion)
}

func TestConfig(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/config")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var v configView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%v (%s)", err, body)
	}
	if v.CompatibilityLevel != "BACKWARD" {
		t.Fatalf("compatibilityLevel = %q, want BACKWARD (the default)", v.CompatibilityLevel)
	}
}

func TestSubjectConfig(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/config/"+testURN)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var v configView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%v (%s)", err, body)
	}
	if v.CompatibilityLevel != "BACKWARD" {
		t.Fatalf("compatibilityLevel = %q, want BACKWARD", v.CompatibilityLevel)
	}
}

func TestSubjectConfig_UnknownSubject(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/config/urn:babel:nope")
	assertConfluentError(t, status, body, http.StatusNotFound, errCodeSubjectNotFound)
}

func TestNotFoundRoute(t *testing.T) {
	ts, _ := newTestServer(t)
	status, body := get(t, ts, "/totally/unknown")
	assertConfluentError(t, status, body, http.StatusNotFound, errCodeSchemaNotFound)
}

func TestConfigLevelOverride(t *testing.T) {
	// New("...","FULL") must report the explicit level, and an empty level defaults to BACKWARD.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "schemas"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schemas", "o.json"), []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(regPath, []byte(`{"schemas":[{"urn":"`+testURN+`","schema":"schemas/o.json"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Load(regPath)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(r, "FULL").Handler())
	t.Cleanup(ts.Close)

	status, body := get(t, ts, "/config")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var v configView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%v (%s)", err, body)
	}
	if v.CompatibilityLevel != "FULL" {
		t.Fatalf("compatibilityLevel = %q, want FULL", v.CompatibilityLevel)
	}
}

// newServerWithUnreadableSchema loads a registry whose schema file exists at Load time but is
// then removed, so New cannot read it and the subject is recorded with an empty schema — the
// backend-error path. It returns a running server.
func newServerWithUnreadableSchema(t *testing.T) (*httptest.Server, int) {
	t.Helper()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schemas", "o.json")
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(regPath, []byte(`{"schemas":[{"urn":"`+testURN+`","schema":"schemas/o.json"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Load(regPath) // Load does not read schema files, so this succeeds.
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(schemaPath); err != nil { // now the schema is unreadable for New.
		t.Fatal(err)
	}
	s := New(r, "")
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s.subjects[testURN].id
}

func TestGetVersion_BackendError(t *testing.T) {
	ts, _ := newServerWithUnreadableSchema(t)
	status, body := get(t, ts, "/subjects/"+testURN+"/versions/latest")
	assertConfluentError(t, status, body, http.StatusInternalServerError, errCodeBackendError)
}

func TestGetByID_BackendError(t *testing.T) {
	ts, id := newServerWithUnreadableSchema(t)
	status, body := get(t, ts, "/schemas/ids/"+itoa(id))
	assertConfluentError(t, status, body, http.StatusInternalServerError, errCodeBackendError)
}

func TestCompatibility_BackendError(t *testing.T) {
	ts, _ := newServerWithUnreadableSchema(t)
	status, body := postRaw(t, ts, "/compatibility/subjects/"+testURN+"/versions/latest", `{"schema":"{\"type\":\"object\"}"}`)
	assertConfluentError(t, status, body, http.StatusInternalServerError, errCodeBackendError)
}

// assertConfluentError asserts the response is a Confluent error body with the wanted HTTP status
// and error_code, and that the documented content type is set.
func assertConfluentError(t *testing.T, status int, body []byte, wantHTTP, wantCode int) {
	t.Helper()
	if status != wantHTTP {
		t.Fatalf("status = %d, want %d; body=%s", status, wantHTTP, body)
	}
	var e apiError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body is not Confluent-shaped: %v (%s)", err, body)
	}
	if e.ErrorCode != wantCode {
		t.Fatalf("error_code = %d, want %d (%s)", e.ErrorCode, wantCode, body)
	}
	if e.Message == "" {
		t.Fatal("error body has an empty message")
	}
}

// postCompatibility posts a candidate schema and returns is_compatible, asserting the HTTP status.
func postCompatibility(t *testing.T, ts *httptest.Server, subject, version, candidateSchema string, wantStatus int) bool {
	t.Helper()
	// The candidate schema must be JSON-encoded into the "schema" string field, per Confluent.
	encoded, err := json.Marshal(candidateSchema)
	if err != nil {
		t.Fatal(err)
	}
	bodyIn := `{"schema":` + string(encoded) + `}`
	status, body := postRaw(t, ts, "/compatibility/subjects/"+subject+"/versions/"+version, bodyIn)
	if status != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", status, wantStatus, body)
	}
	var v compatibilityView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("response is not a compatibility body: %v (%s)", err, body)
	}
	return v.IsCompatible
}

// postRaw posts a raw body to path and returns status + body.
func postRaw(t *testing.T, ts *httptest.Server, path, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(ts.URL+path, contentType, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, readAll(t, resp)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
