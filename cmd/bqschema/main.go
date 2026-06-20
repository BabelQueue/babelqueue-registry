// Command bqschema is a file-based, broker-free per-URN schema governance CLI for
// BabelQueue: it validates a message's `data` against its URN's registered draft-07
// schema, and lints backward-compatibility between two versions of a schema (the CI gate
// that enforces versioning-policy §3). No Kafka, no service — schemas live in git.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/babelqueue/babelqueue-registry/internal/asyncapi"
	"github.com/babelqueue/babelqueue-registry/internal/compat"
	"github.com/babelqueue/babelqueue-registry/internal/gdpr"
	"github.com/babelqueue/babelqueue-registry/internal/registry"
	"github.com/babelqueue/babelqueue-registry/internal/restapi"
	"github.com/babelqueue/babelqueue-registry/internal/schema"
)

const usage = `bqschema — file-based per-URN schema governance for BabelQueue

Usage:
  bqschema validate --registry <registry.json> <envelope.json>...  validate a message's data against its URN schema
  bqschema compat   <old-schema.json> <new-schema.json>            check backward-compatibility (exit 1 on a breaking change)
  bqschema check    --registry <registry.json>                     validate the registry itself (every schema parses)
  bqschema export-asyncapi --registry <registry.json> [-o out.json] emit an AsyncAPI 3.0 event catalog from the registry
  bqschema serve    --registry <registry.json> [--addr :8081]      serve a Confluent-compatible (read-mostly) REST API over the registry
  bqschema gdpr     --registry <registry.json> [--require] [--mask m.json] audit x-gdpr-sensitive fields (and mask a message)

Schemas are draft-07 JSON Schema (subset) for the envelope's "data" block.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "validate":
		os.Exit(runValidate(os.Args[2:]))
	case "compat":
		os.Exit(runCompat(os.Args[2:]))
	case "check":
		os.Exit(runCheck(os.Args[2:]))
	case "export-asyncapi":
		os.Exit(runExport(os.Args[2:]))
	case "serve":
		os.Exit(runServe(os.Args[2:]))
	case "gdpr":
		os.Exit(runGDPR(os.Args[2:]))
	case "-h", "--help", "help":
		fmt.Println(usage)
	default:
		fmt.Fprintf(os.Stderr, "bqschema: unknown command %q\n\n%s\n", os.Args[1], usage)
		os.Exit(2)
	}
}

func runValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	reg := fs.String("registry", "registry.json", "path to the registry manifest")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		return fail("validate: pass one or more envelope JSON files")
	}
	r, err := registry.Load(*reg)
	if err != nil {
		return fail("%v", err)
	}

	failed := false
	for _, file := range fs.Args() {
		raw, err := os.ReadFile(file)
		if err != nil {
			return fail("read %s: %v", file, err)
		}
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			return fail("%s: invalid JSON: %v", file, err)
		}
		urn := stringField(env, "job")
		if urn == "" {
			urn = stringField(env, "urn")
		}
		if urn == "" {
			fmt.Printf("%s: no job/urn — skipped\n", file)
			continue
		}
		s, ok, err := r.Schema(urn)
		if err != nil {
			return fail("%v", err)
		}
		if !ok {
			fmt.Printf("%s [%s]: no schema registered — skipped\n", file, urn)
			continue
		}
		errs := s.Validate(env["data"])
		if len(errs) == 0 {
			fmt.Printf("%s [%s]: ok\n", file, urn)
			continue
		}
		failed = true
		fmt.Printf("%s [%s]: %d violation(s)\n", file, urn, len(errs))
		for _, e := range errs {
			fmt.Printf("  - %s\n", e)
		}
	}
	if failed {
		return 1
	}
	return 0
}

func runCompat(args []string) int {
	fs := flag.NewFlagSet("compat", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() != 2 {
		return fail("compat: need exactly two schema files: <old> <new>")
	}
	oldS, err := loadSchema(fs.Arg(0))
	if err != nil {
		return fail("%v", err)
	}
	newS, err := loadSchema(fs.Arg(1))
	if err != nil {
		return fail("%v", err)
	}

	breaks := compat.Check(oldS, newS)
	if len(breaks) == 0 {
		fmt.Println("backward-compatible: no breaking changes.")
		return 0
	}
	fmt.Printf("%d breaking change(s) — mint a new URN instead (versioning-policy §3):\n", len(breaks))
	for _, b := range breaks {
		fmt.Printf("  - %s\n", b)
	}
	return 1
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	reg := fs.String("registry", "registry.json", "path to the registry manifest")
	_ = fs.Parse(args)
	r, err := registry.Load(*reg)
	if err != nil {
		return fail("%v", err)
	}

	urns := r.URNs()
	bad := false
	for _, urn := range urns {
		if _, _, err := r.Schema(urn); err != nil {
			bad = true
			fmt.Printf("  x %s: %v\n", urn, err)
			continue
		}
		fmt.Printf("  ok %s\n", urn)
	}
	fmt.Printf("%d URN(s) in registry.\n", len(urns))
	if bad {
		return 1
	}
	return 0
}

func runExport(args []string) int {
	fs := flag.NewFlagSet("export-asyncapi", flag.ExitOnError)
	reg := fs.String("registry", "registry.json", "path to the registry manifest")
	out := fs.String("o", "", "write to this file instead of stdout")
	title := fs.String("title", "BabelQueue Event Catalog", "AsyncAPI info.title")
	docVersion := fs.String("doc-version", "1.0.0", "AsyncAPI info.version")
	_ = fs.Parse(args)

	r, err := registry.Load(*reg)
	if err != nil {
		return fail("%v", err)
	}
	doc, err := asyncapi.Build(*title, *docVersion, r)
	if err != nil {
		return fail("%v", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fail("marshal asyncapi: %v", err)
	}
	data = append(data, '\n')

	if *out == "" {
		fmt.Print(string(data))
		return 0
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return fail("write %s: %v", *out, err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d URN(s))\n", *out, len(r.URNs()))
	return 0
}

// listenAndServe is the seam runServe binds through; production uses http.ListenAndServe, tests
// substitute a non-blocking stub. It normally only returns on error (it blocks while serving).
var listenAndServe = http.ListenAndServe

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	reg := fs.String("registry", "registry.json", "path to the registry manifest")
	addr := fs.String("addr", ":8081", "address to listen on (host:port)")
	level := fs.String("compatibility", "BACKWARD", "compatibility level reported by /config")
	_ = fs.Parse(args)

	r, err := registry.Load(*reg)
	if err != nil {
		return fail("%v", err)
	}
	srv := restapi.New(r, *level)

	fmt.Fprintf(os.Stderr, "bqschema serve: Confluent-compatible REST on %s (%d subject(s), compatibility=%s)\n",
		*addr, len(r.URNs()), *level)
	// listenAndServe only returns on error (it blocks serving otherwise), so any return here is a
	// failure to bind/serve — surface it as an IO error (exit 2), consistent with the other commands.
	if err := listenAndServe(*addr, srv.Handler()); err != nil {
		return fail("serve: %v", err)
	}
	return 0
}

// runGDPR audits the registry's x-gdpr-sensitive coverage. Three modes share one command:
//
//   - default: print an inventory (per-URN sensitive paths + a coverage summary). Exit 0.
//   - --require [--pattern <re>]: fail (exit 1) if any property whose NAME matches the PII pattern
//     is NOT marked x-gdpr-sensitive — the CI gate that catches un-annotated PII.
//   - --mask <message.json>: print a copy of the message with sensitive fields masked. The message
//     may be a full envelope (its "data" is masked against the URN in "job"/"urn") or a bare data
//     object (masked against --urn). Exit 0; exit 1 only when the masked URN has no schema.
//
// Exit codes follow the repo convention: 0 ok, 1 audit failure / unmaskable message, 2 usage/IO.
func runGDPR(args []string) int {
	fs := flag.NewFlagSet("gdpr", flag.ExitOnError)
	reg := fs.String("registry", "registry.json", "path to the registry manifest")
	require := fs.Bool("require", false, "fail (exit 1) if a PII-named field is not marked x-gdpr-sensitive")
	pattern := fs.String("pattern", "", "PII name regexp for --require (default: built-in email/ssn/phone/tckn/…)")
	mask := fs.String("mask", "", "mask the sensitive fields of this message JSON and print the result")
	urn := fs.String("urn", "", "URN to mask against when --mask is a bare data object (no job/urn field)")
	_ = fs.Parse(args)

	r, err := registry.Load(*reg)
	if err != nil {
		return fail("%v", err)
	}

	if *mask != "" {
		return runGDPRMask(r, *mask, *urn)
	}
	if *require {
		return runGDPRRequire(r, *pattern)
	}
	return runGDPRInventory(r)
}

func runGDPRInventory(r *registry.Registry) int {
	inv, err := gdpr.BuildInventory(r)
	if err != nil {
		return fail("%v", err)
	}
	for _, u := range inv.URNs {
		if len(u.Paths) == 0 {
			fmt.Printf("  %s: no sensitive fields\n", u.URN)
			continue
		}
		fmt.Printf("  %s: %d sensitive field(s)\n", u.URN, len(u.Paths))
		for _, p := range u.Paths {
			if p.Category != "" {
				fmt.Printf("    - %s (%s)\n", p.Path, p.Category)
			} else {
				fmt.Printf("    - %s\n", p.Path)
			}
		}
	}
	fmt.Printf("%d URN(s), %d sensitive field(s).\n", inv.TotalURNs, inv.TotalFields)
	return 0
}

func runGDPRRequire(r *registry.Registry, pattern string) int {
	res, err := gdpr.Audit(r, pattern)
	if err != nil {
		return fail("%v", err)
	}
	if res.OK() {
		fmt.Printf("ok: every PII-named field across %d URN(s) is marked x-gdpr-sensitive.\n", res.CheckedURNs)
		return 0
	}
	fmt.Printf("%d un-annotated PII field(s) — add \"x-gdpr-sensitive\": true (or mint the URN deliberately):\n", len(res.Findings))
	for _, f := range res.Findings {
		fmt.Printf("  x %s: %s\n", f.URN, f.Path)
	}
	return 1
}

func runGDPRMask(r *registry.Registry, messageFile, urnFlag string) int {
	raw, err := os.ReadFile(messageFile)
	if err != nil {
		return fail("read %s: %v", messageFile, err)
	}
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fail("%s: invalid JSON: %v", messageFile, err)
	}

	// A full envelope masks its "data" against the URN in job/urn; a bare object masks itself
	// against --urn.
	urn := urnFlag
	target := msg
	isEnvelope := false
	if _, hasData := msg["data"]; hasData {
		isEnvelope = true
		if urn == "" {
			urn = stringField(msg, "job")
			if urn == "" {
				urn = stringField(msg, "urn")
			}
		}
	}
	if urn == "" {
		return fail("gdpr --mask: no URN — pass --urn, or use an envelope with a job/urn field")
	}

	s, ok, err := r.Schema(urn)
	if err != nil {
		return fail("%v", err)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "bqschema: gdpr --mask: no schema registered for %q\n", urn)
		return 1
	}

	var out any
	if isEnvelope {
		masked := make(map[string]any, len(msg))
		for k, v := range msg {
			masked[k] = v
		}
		masked["data"] = gdpr.Mask(msg["data"], s)
		out = masked
	} else {
		out = gdpr.Mask(target, s)
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fail("marshal masked message: %v", err)
	}
	fmt.Println(string(data))
	return 0
}

func loadSchema(path string) (*schema.Schema, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return schema.Parse(raw)
}

func stringField(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "bqschema: "+format+"\n", args...)
	return 2
}
