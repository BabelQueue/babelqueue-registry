// Command bqschema is a file-based, broker-free per-URN schema governance CLI for
// BabelQueue: it validates a message's `data` against its URN's registered draft-07
// schema, and lints backward-compatibility between two versions of a schema (the CI gate
// that enforces versioning-policy §3). No Kafka, no service — schemas live in git.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/babelqueue/babelqueue-registry/internal/asyncapi"
	"github.com/babelqueue/babelqueue-registry/internal/compat"
	"github.com/babelqueue/babelqueue-registry/internal/registry"
	"github.com/babelqueue/babelqueue-registry/internal/schema"
)

const usage = `bqschema — file-based per-URN schema governance for BabelQueue

Usage:
  bqschema validate --registry <registry.json> <envelope.json>...  validate a message's data against its URN schema
  bqschema compat   <old-schema.json> <new-schema.json>            check backward-compatibility (exit 1 on a breaking change)
  bqschema check    --registry <registry.json>                     validate the registry itself (every schema parses)
  bqschema export-asyncapi --registry <registry.json> [-o out.json] emit an AsyncAPI 3.0 event catalog from the registry

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
