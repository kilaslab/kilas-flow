// Package tenantsettings defines the per-tenant settings document: the values
// an operator changes for one tenant at runtime, as opposed to the deployment
// configuration every tenant shares and the process reads once at boot.
//
// The document is a JSON object of sections. Each section belongs to one
// consumer package, which registers it with a Go struct type and a validator;
// this package owns only the merge rules, so a new consumer adds a section
// without touching the update endpoint. A key no consumer registered is
// refused rather than stored, so a typo cannot sit in the document looking like
// a setting that applies.
//
// Sections evolve additively: a new optional field is safe, because a stored
// document without it decodes with the field unset. A change that would
// reinterpret stored values is a new section name, with the old one retired by
// patching it to null.
//
// This package imports only the standard library. The repository stores the
// bytes; consumers import this package, never the other way round.
package tenantsettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Problem codes the update endpoint attaches to a refused patch.
const (
	// CodeUnknownKey names a section no consumer registered, or a field its
	// section does not declare.
	CodeUnknownKey = "tenant_settings_unknown_key"
	// CodeInvalid names a value its section refused: not an object, the wrong
	// type, or outside what the section's validator allows.
	CodeInvalid = "tenant_settings_invalid"
)

// Issue is one reason a patch was refused.
type Issue struct {
	Code    string
	Path    string
	Message string
}

// PatchError reports every reason a patch was refused. Nothing is written
// when Apply returns one.
type PatchError struct {
	Issues []Issue
}

func (err *PatchError) Error() string {
	parts := make([]string, 0, len(err.Issues))
	for _, issue := range err.Issues {
		parts = append(parts, issue.Path+": "+issue.Message)
	}
	return "tenant settings refused: " + strings.Join(parts, "; ")
}

var sectionNamePattern = regexp.MustCompile(`^[a-z][a-zA-Z0-9]{0,63}$`)

// section is one registered consumer's part of the document.
type section struct {
	name   string
	fields map[string]bool
	// decode turns a merged section object into the canonical stored form
	// and the typed value, running the consumer's validator.
	decode func(raw []byte) (any, []byte, error)
}

// Registry holds the sections this process accepts.
type Registry struct {
	mu       sync.RWMutex
	sections map[string]*section
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{sections: map[string]*section{}}
}

// Register adds a section named name whose value is a T. T must be a struct
// whose exported fields carry json tags; those tags are the section's keys.
// validate, when non-nil, refuses a merged value before it is stored.
func Register[T any](r *Registry, name string, validate func(T) error) error {
	if r == nil {
		return errors.New("tenant settings: register on a nil registry")
	}
	if !sectionNamePattern.MatchString(name) {
		return fmt.Errorf("tenant settings: section name %q must be lower camelCase letters and digits, at most 64", name)
	}
	typ := reflect.TypeOf((*T)(nil)).Elem()
	if typ.Kind() != reflect.Struct {
		return fmt.Errorf("tenant settings: section %q must be a struct, not %s", name, typ.Kind())
	}
	fields := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Anonymous {
			return fmt.Errorf("tenant settings: section %q embeds %s; declare its fields directly", name, field.Type)
		}
		if !field.IsExported() {
			continue
		}
		key := field.Name
		if tag, ok := field.Tag.Lookup("json"); ok {
			tagName, _, _ := strings.Cut(tag, ",")
			if tagName == "-" {
				continue
			}
			if tagName != "" {
				key = tagName
			}
		}
		fields[key] = true
	}
	decode := func(raw []byte) (any, []byte, error) {
		var value T
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, err
		}
		if validate != nil {
			if err := validate(value); err != nil {
				return nil, nil, err
			}
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return nil, nil, err
		}
		return value, canonical, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, taken := r.sections[name]; taken {
		return fmt.Errorf("tenant settings: section %q is already registered", name)
	}
	r.sections[name] = &section{name: name, fields: fields, decode: decode}
	return nil
}

// Sections lists the registered section names, sorted.
func (r *Registry) Sections() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.sections))
	for name := range r.sections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) lookup(name string) *section {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sections[name]
}

// Apply merges patch into the stored document and returns the new document.
//
// Each section merges as an RFC 7396 merge patch: a section set to null is
// removed, so the tenant falls back to the deployment configuration for it; an
// object is merged field by field, a field set to null removing that field.
// The merged section is decoded into its type, validated and stored in
// canonical form; a section left with no fields is dropped, and a document
// left with no sections is returned as nil.
//
// Every refusal is collected into one *PatchError, and the stored document is
// then left as it was.
func (r *Registry) Apply(stored []byte, patch map[string]json.RawMessage) ([]byte, error) {
	document, err := splitDocument(stored)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var issues []Issue
	for _, name := range keys {
		raw := patch[name]
		isNull := isJSONNull(raw)
		sec := r.lookup(name)
		if sec == nil {
			if _, present := document[name]; present && isNull {
				// Retiring a section its consumer no longer registers.
				delete(document, name)
				continue
			}
			issues = append(issues, Issue{Code: CodeUnknownKey, Path: name, Message: fmt.Sprintf("%q is not a settings section this server accepts", name)})
			continue
		}
		if isNull {
			delete(document, name)
			continue
		}
		var fieldPatch map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fieldPatch); err != nil || fieldPatch == nil {
			issues = append(issues, Issue{Code: CodeInvalid, Path: name, Message: "a settings section must be a JSON object or null"})
			continue
		}
		current := map[string]json.RawMessage{}
		if existing, ok := document[name]; ok {
			_ = json.Unmarshal(existing, &current)
		}
		fieldKeys := make([]string, 0, len(fieldPatch))
		for key := range fieldPatch {
			fieldKeys = append(fieldKeys, key)
		}
		sort.Strings(fieldKeys)
		refused := false
		for _, key := range fieldKeys {
			if !sec.fields[key] {
				issues = append(issues, Issue{Code: CodeUnknownKey, Path: name + "." + key, Message: fmt.Sprintf("%q is not a field of the %s section", key, name)})
				refused = true
				continue
			}
			if isJSONNull(fieldPatch[key]) {
				delete(current, key)
				continue
			}
			current[key] = fieldPatch[key]
		}
		if refused {
			continue
		}
		if len(current) == 0 {
			delete(document, name)
			continue
		}
		merged, _ := json.Marshal(current)
		_, canonical, err := sec.decode(merged)
		if err != nil {
			issues = append(issues, decodeIssue(name, err))
			continue
		}
		if string(canonical) == "{}" {
			delete(document, name)
			continue
		}
		document[name] = canonical
	}
	if len(issues) > 0 {
		return nil, &PatchError{Issues: issues}
	}
	if len(document) == 0 {
		return nil, nil
	}
	// json.Marshal sorts map keys, so equal documents are equal bytes and a
	// no-op patch is recognisable by comparison.
	out, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("tenant settings: encode document: %w", err)
	}
	return out, nil
}

// decodeIssue turns a section's decode or validation failure into an Issue,
// naming the field where encoding/json says which one it was.
func decodeIssue(name string, err error) Issue {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return Issue{Code: CodeInvalid, Path: name + "." + typeErr.Field, Message: fmt.Sprintf("expected %s, got a JSON %s", typeErr.Type, typeErr.Value)}
	}
	return Issue{Code: CodeInvalid, Path: name, Message: err.Error()}
}

// splitDocument reads a stored document into its sections, raw.
func splitDocument(stored []byte) (map[string]json.RawMessage, error) {
	document := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(stored)) == 0 {
		return document, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(stored))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("tenant settings: stored document is not a JSON object: %w", err)
	}
	if document == nil {
		document = map[string]json.RawMessage{}
	}
	return document, nil
}

func isJSONNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

// Settings is one tenant's decoded settings document at one version.
type Settings struct {
	version  int64
	sections map[string]any
}

// Version is the settings_version the document was read at; 0 means the
// tenant has never had settings written.
func (s Settings) Version() int64 { return s.version }

// Decode reads a stored document into typed sections. A section that no
// longer decodes or validates (or is no longer registered) is left out and
// reported in the returned error alongside the rest, so a reader can log it
// and carry on with the deployment default for that section.
func (r *Registry) Decode(stored []byte, version int64) (Settings, error) {
	settings := Settings{version: version, sections: map[string]any{}}
	document, err := splitDocument(stored)
	if err != nil {
		return settings, err
	}
	var problems []error
	for name, raw := range document {
		sec := r.lookup(name)
		if sec == nil {
			continue
		}
		value, _, err := sec.decode(raw)
		if err != nil {
			problems = append(problems, fmt.Errorf("section %s: %w", name, err))
			continue
		}
		settings.sections[name] = value
	}
	return settings, errors.Join(problems...)
}

// Lookup returns the named section of s as a T, and whether the tenant set it.
func Lookup[T any](s Settings, name string) (T, bool) {
	value, ok := s.sections[name].(T)
	return value, ok
}
