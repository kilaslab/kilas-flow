// Command config-reference regenerates the operator-facing configuration
// reference and the example YAML from the Config structs in
// internal/config, so a new field appears in the documentation without
// anyone remembering to add it.
//
// Usage:
//
//	go run ./scripts/config-reference.go          # regenerate both files
//	go run ./scripts/config-reference.go --check  # fail if either is stale
//
// The reference reads koanf tags for keys, reflection over Default() for
// types and defaults, and the doc comments for prose. A field without a doc
// comment or without a koanf tag fails the run: that is the check that keeps
// a new setting from shipping undocumented.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/config"
)

var durationType = reflect.TypeOf(time.Duration(0))

// requiredConditions names the keys that must be set. Everything else is
// optional. Keys are "section.key"; an entry for a field that does not exist
// fails the run, so this map cannot drift silently.
var requiredConditions = map[string]string{
	"database.dsn":                "yes — boot fails when it is empty",
	"auth.signing_key_env":        "yes, when auth.enabled is true — boot is refused without a key",
	"auth.bootstrap_email":        "together with auth.enabled and a bootstrap password, on the first start only",
	"auth.bootstrap_password_env": "together with auth.enabled and a bootstrap email, on the first start only",
}

type field struct {
	key      string
	typeName string
	defValue string
	env      string
	required string
	doc      string
}

type section struct {
	name   string
	doc    string
	fields []field
}

func main() {
	check := flag.Bool("check", false, "compare generated output against the committed files and fail on difference")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintf(os.Stderr, "config-reference: %v\n", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	sections, err := buildModel(filepath.Join(root, "internal", "config", "config.go"))
	if err != nil {
		return err
	}
	outputs := map[string]string{
		filepath.Join(root, "docs", "src", "content", "docs", "operate", "configuration-reference.md"): renderReference(sections),
		filepath.Join(root, "config.example.yaml"):                                                     renderExample(sections),
	}
	// Deterministic order so --check output reads the same everywhere.
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	sort.Strings(names)
	failed := false
	for _, name := range names {
		want := outputs[name]
		if !check {
			if err := os.WriteFile(name, []byte(want), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", name, err)
			}
			continue
		}
		got, err := os.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if string(got) != want {
			fmt.Fprintf(os.Stderr, "config-reference: %s is stale. Run `go run ./scripts/config-reference.go` and commit the result.\n", name)
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("generated configuration files are stale")
	}
	return nil
}

// repoRoot resolves the repository root from this file's own path, so the
// generator works regardless of the caller's working directory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate source file")
	}
	return filepath.Dir(filepath.Dir(file)), nil
}

// buildModel walks the Config struct by reflection for shape and defaults,
// and parses the source for the doc comments that become operator prose.
func buildModel(sourcePath string) ([]section, error) {
	docs, err := parseDocs(sourcePath)
	if err != nil {
		return nil, err
	}
	cfg := config.Default()
	root := reflect.TypeOf(cfg)
	if root.Kind() != reflect.Struct {
		return nil, fmt.Errorf("config.Config is not a struct")
	}
	def := reflect.ValueOf(cfg)
	sections := make([]section, 0, root.NumField())
	for i := range root.NumField() {
		sf := root.Field(i)
		tag, ok := sf.Tag.Lookup("koanf")
		if !ok || tag == "" {
			return nil, fmt.Errorf("section %s has no koanf tag", sf.Name)
		}
		st := sf.Type
		if st.Kind() != reflect.Struct {
			return nil, fmt.Errorf("section %s is not a struct", tag)
		}
		sv := def.Field(i)
		sec := section{name: tag, doc: docs.sectionDoc(st.Name())}
		if sec.doc == "" {
			return nil, fmt.Errorf("section %s (%s) has no doc comment", tag, sf.Name)
		}
		for j := range st.NumField() {
			ff := st.Field(j)
			key, ok := ff.Tag.Lookup("koanf")
			if !ok || key == "" {
				return nil, fmt.Errorf("field %s.%s has no koanf tag", tag, ff.Name)
			}
			doc := docs.fieldDoc(st.Name(), ff.Name)
			if doc == "" {
				return nil, fmt.Errorf("field %s.%s has no doc comment", tag, key)
			}
			fv := sv.Field(j)
			typeName, defValue, err := describeValue(fv)
			if err != nil {
				return nil, fmt.Errorf("field %s.%s: %w", tag, key, err)
			}
			required := "no"
			if cond, ok := requiredConditions[tag+"."+key]; ok {
				required = cond
			}
			sec.fields = append(sec.fields, field{
				key:      key,
				typeName: typeName,
				defValue: defValue,
				env:      "KILASFLOW_" + strings.ToUpper(tag) + "_" + strings.ToUpper(key),
				required: required,
				doc:      doc,
			})
		}
		sections = append(sections, sec)
	}
	for key := range requiredConditions {
		seen := false
		for _, sec := range sections {
			for _, f := range sec.fields {
				if sec.name+"."+f.key == key {
					seen = true
				}
			}
		}
		if !seen {
			return nil, fmt.Errorf("requiredConditions names %q, which matches no field", key)
		}
	}
	return sections, nil
}

// describeValue renders a Default() value as its type label and its display
// default for both outputs.
func describeValue(v reflect.Value) (typeName, defValue string, err error) {
	if v.Type() == durationType {
		d := time.Duration(v.Int())
		if d == 0 {
			return "duration", "0", nil
		}
		return "duration", d.String(), nil
	}
	switch v.Kind() {
	case reflect.String:
		return "string", "'" + strings.ReplaceAll(v.String(), "'", "''") + "'", nil
	case reflect.Bool:
		if v.Bool() {
			return "bool", "true", nil
		}
		return "bool", "false", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		unit := "integer"
		if v.Type().Kind() == reflect.Int64 {
			unit = "integer (bytes)"
		}
		return unit, fmt.Sprintf("%d", v.Int()), nil
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String {
			return "", "", fmt.Errorf("unsupported slice element %s", v.Type().Elem())
		}
		if v.Len() == 0 {
			return "string list", "[]", nil
		}
		items := make([]string, 0, v.Len())
		for i := range v.Len() {
			items = append(items, "'"+strings.ReplaceAll(v.Index(i).String(), "'", "''")+"'")
		}
		return "string list", "[" + strings.Join(items, ", ") + "]", nil
	default:
		return "", "", fmt.Errorf("unsupported kind %s", v.Kind())
	}
}

// docs holds the parsed doc comments keyed by struct and field name.
type docs struct {
	sections map[string]string
	fields   map[string]map[string]string
}

func (d docs) sectionDoc(structName string) string {
	return d.sections[structName]
}

func (d docs) fieldDoc(structName, fieldName string) string {
	return d.fields[structName][fieldName]
}

// parseDocs reads struct and field doc comments out of the config source.
// Lines starting with "Env:" or "Default:" are dropped: the generator renders
// those as table columns from the tags and Default(), and keeping them in the
// prose would maintain the same fact twice.
func parseDocs(sourcePath string) (docs, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, sourcePath, nil, parser.ParseComments)
	if err != nil {
		return docs{}, fmt.Errorf("parse %s: %w", sourcePath, err)
	}
	out := docs{sections: map[string]string{}, fields: map[string]map[string]string{}}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			out.sections[ts.Name.Name] = cleanComment(gen.Doc)
			m := map[string]string{}
			for _, f := range st.Fields.List {
				if len(f.Names) == 0 {
					continue
				}
				m[f.Names[0].Name] = cleanComment(f.Doc)
			}
			out.fields[ts.Name.Name] = m
		}
	}
	return out, nil
}

func cleanComment(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	lines := make([]string, 0, len(group.List))
	for _, c := range group.List {
		text := strings.TrimPrefix(c.Text, "//")
		text = strings.TrimPrefix(text, " ")
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "Env:") || strings.HasPrefix(trimmed, "Default:") {
			continue
		}
		lines = append(lines, strings.TrimRight(text, " \t"))
	}
	// Collapse runs of blank lines; drop leading/trailing blanks.
	out := make([]string, 0, len(lines))
	blank := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			if !blank {
				out = append(out, "")
				blank = true
			}
			continue
		}
		out = append(out, l)
		blank = false
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func renderReference(sections []section) string {
	var b strings.Builder
	b.WriteString(`---
title: Configuration reference
description: Every KilasFlow configuration key, generated from the config struct.
sidebar:
  order: 3
---

<!-- Generated by scripts/config-reference.go. Do not edit by hand. -->

Every key the server reads, with its type, its default, its environment
variable and whether it is required. This page is generated from
` + "`internal/config/config.go`" + ` — a new field appears here when the
struct gains one, and a section can never be absent.

Precedence is defaults, then the configuration file, then ` + "`KILASFLOW_*`" + `
environment variables; only the first underscore after the prefix separates the
section from the key. The [Configuration](/operate/configuration/) guide
explains that rule, the ` + "`KILASFLOW_WORKFLOW_ENV_*`" + ` allowlist, and what
the server does when a key is missing.

Regenerate this page with ` + "`make generate-config-reference`" + ` after changing
` + "`internal/config/config.go`" + `; ` + "`make generate-config-reference-check`" + `
(also run in CI) fails when the committed page is stale.
`)
	for _, sec := range sections {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", sec.name, sec.doc)
		for _, f := range sec.fields {
			fmt.Fprintf(&b, "\n### %s.%s\n\n", sec.name, f.key)
			fmt.Fprintf(&b, "- Type: `%s`\n- Default: `%s`\n- Environment: `%s`\n- Required: %s\n\n%s\n",
				f.typeName, f.defValue, f.env, f.required, f.doc)
		}
	}
	return b.String()
}

func renderExample(sections []section) string {
	var b strings.Builder
	b.WriteString(`# kilasflow configuration.
#
# GENERATED by scripts/config-reference.go from internal/config/config.go.
# Do not edit the structure by hand: copy this file to config.yaml and change
# values there, or set KILASFLOW_* environment variables. Regenerating
# overwrites this file.
#
# Every key can be overridden by an environment variable named
# KILASFLOW_<SECTION>_<KEY>, for example KILASFLOW_SERVER_PORT=9090 or
# KILASFLOW_DATABASE_DRIVER=postgres. Only the first underscore after the
# prefix separates the section from the key. Running without any file is fully
# supported.
`)
	for _, sec := range sections {
		fmt.Fprintf(&b, "\n%s:\n", sec.name)
		for _, f := range sec.fields {
			for _, line := range strings.Split(f.doc, "\n") {
				if strings.TrimSpace(line) == "" {
					b.WriteString("  #\n")
					continue
				}
				fmt.Fprintf(&b, "  # %s\n", line)
			}
			fmt.Fprintf(&b, "  # Env: %s\n", f.env)
			fmt.Fprintf(&b, "  %s: %s\n", f.key, f.defValue)
		}
		if sec.name == "database" {
			b.WriteString(`
  # PostgreSQL example (uncomment to use; switching backends does not move
  # data — the schema is created fresh from migrations/postgres, so decide
  # before the first run):
  # driver: postgres
  # dsn: postgres://kilasflow:password@localhost:5432/kilasflow?sslmode=disable
`)
		}
	}
	return b.String()
}
