// Package payload provides adaptive API response schema validation for
// GoSessionEngine.
//
// Target APIs occasionally change their response structure without notice:
// fields are renamed, new required fields are added, or the type of an
// existing field changes (e.g. a number becomes a string).  Any of these
// changes can silently corrupt downstream processing.
//
// This package implements a lightweight schema-snapshot mechanism:
//
//  1. On the first successful response, Validator.Learn records the field
//     names and their JSON types as the baseline schema.
//
//  2. On every subsequent response, Validator.Validate compares the current
//     response against the baseline and returns a list of Mismatch records
//     describing any structural differences.
//
//  3. Callers pass each Mismatch to their logging/alerting pipeline so
//     operators can investigate before the change propagates silently.
//
// The validator works on flat and nested JSON objects.  Nested keys are
// represented as dot-separated paths (e.g. "user.address.zip").
//
// # Thread safety
//
// Validator is safe for concurrent use: a sync.RWMutex protects the baseline
// snapshot.  Multiple goroutines may call Validate simultaneously; Learn
// acquires an exclusive write-lock only when updating the baseline.
package payload

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// MismatchKind classifies the type of schema difference detected.
type MismatchKind string

const (
	// MismatchKindMissing indicates a field present in the baseline is absent
	// in the current response.
	MismatchKindMissing MismatchKind = "MISSING_FIELD"

	// MismatchKindAdded indicates a field not present in the baseline was
	// added to the current response.
	MismatchKindAdded MismatchKind = "ADDED_FIELD"

	// MismatchKindTypeChange indicates a field exists in both but its JSON
	// type changed (e.g. "number" → "string").
	MismatchKindTypeChange MismatchKind = "TYPE_CHANGE"
)

// Mismatch describes a single structural difference between the baseline
// schema and a current API response.
type Mismatch struct {
	// Kind classifies the mismatch.
	Kind MismatchKind

	// Field is the dot-separated path to the affected field.
	Field string

	// BaselineType is the JSON type recorded in the baseline ("string",
	// "number", "bool", "array", "object", "null").  Empty for MismatchKindAdded.
	BaselineType string

	// CurrentType is the JSON type in the current response.  Empty for
	// MismatchKindMissing.
	CurrentType string
}

// String returns a human-readable description suitable for CMD output.
func (m Mismatch) String() string {
	switch m.Kind {
	case MismatchKindMissing:
		return fmt.Sprintf("PAYLOAD MISMATCH [%s] field %q missing (was %s)", m.Kind, m.Field, m.BaselineType)
	case MismatchKindAdded:
		return fmt.Sprintf("PAYLOAD MISMATCH [%s] field %q added (type %s)", m.Kind, m.Field, m.CurrentType)
	case MismatchKindTypeChange:
		return fmt.Sprintf("PAYLOAD MISMATCH [%s] field %q type changed %s → %s", m.Kind, m.Field, m.BaselineType, m.CurrentType)
	default:
		return fmt.Sprintf("PAYLOAD MISMATCH [%s] field %q", m.Kind, m.Field)
	}
}

// schema maps dot-separated field paths to their JSON type names.
type schema map[string]string

// Validator learns the structure of an API response and detects subsequent
// changes.
type Validator struct {
	baseline schema
	mu       sync.RWMutex
}

// NewValidator creates a Validator with no baseline.  The first call to Learn
// or SetBaseline establishes the reference schema.
func NewValidator() *Validator {
	return &Validator{}
}

// Learn parses data as a JSON object, extracts its field schema, and stores it
// as the new baseline.  Any previous baseline is replaced.
//
// Call Learn once on the first successful API response.  Subsequent responses
// should be compared using Validate.
func (v *Validator) Learn(data []byte) error {
	s, err := extractSchema(data)
	if err != nil {
		return fmt.Errorf("payload: learn schema: %w", err)
	}
	v.mu.Lock()
	v.baseline = s
	v.mu.Unlock()
	return nil
}

// HasBaseline reports whether a baseline schema has been established.
func (v *Validator) HasBaseline() bool {
	v.mu.RLock()
	ok := v.baseline != nil
	v.mu.RUnlock()
	return ok
}

// Validate compares data against the baseline schema and returns any
// mismatches.  An empty slice means the response matches the baseline
// perfectly.
//
// Returns an error if data cannot be parsed as a JSON object.  If no baseline
// has been set (HasBaseline returns false) it calls Learn automatically and
// returns an empty mismatch list.
func (v *Validator) Validate(data []byte) ([]Mismatch, error) {
	current, err := extractSchema(data)
	if err != nil {
		return nil, fmt.Errorf("payload: validate: %w", err)
	}

	v.mu.Lock()
	if v.baseline == nil {
		v.baseline = current
		v.mu.Unlock()
		return nil, nil
	}
	baseline := copySchema(v.baseline)
	v.mu.Unlock()

	return diffSchemas(baseline, current), nil
}

// BaselineFields returns a sorted list of dot-separated field paths recorded
// in the baseline.  Useful for introspection and logging.
func (v *Validator) BaselineFields() []string {
	v.mu.RLock()
	b := copySchema(v.baseline)
	v.mu.RUnlock()

	fields := make([]string, 0, len(b))
	for k := range b {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	return fields
}

// Reset clears the baseline, allowing Learn to start fresh.
func (v *Validator) Reset() {
	v.mu.Lock()
	v.baseline = nil
	v.mu.Unlock()
}

// extractSchema recursively walks a JSON value and returns a map of
// dot-separated paths to their JSON type names.
func extractSchema(data []byte) (schema, error) {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w", err)
	}
	obj, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("expected JSON object, got %T", raw)
	}
	s := make(schema)
	flattenSchema(obj, "", s)
	return s, nil
}

// flattenSchema recursively adds entries to s for every leaf and object node.
func flattenSchema(obj map[string]interface{}, prefix string, s schema) {
	for k, v := range obj {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]interface{}:
			s[path] = "object"
			flattenSchema(val, path, s)
		case []interface{}:
			s[path] = "array"
		case string:
			s[path] = "string"
		case float64:
			s[path] = "number"
		case bool:
			s[path] = "bool"
		case nil:
			s[path] = "null"
		default:
			s[path] = "unknown"
		}
	}
}

// diffSchemas compares baseline against current and returns all detected
// mismatches.
func diffSchemas(baseline, current schema) []Mismatch {
	var mismatches []Mismatch

	// Fields present in baseline but missing or type-changed in current.
	for field, bType := range baseline {
		cType, ok := current[field]
		if !ok {
			mismatches = append(mismatches, Mismatch{
				Kind:         MismatchKindMissing,
				Field:        field,
				BaselineType: bType,
			})
			continue
		}
		if cType != bType {
			mismatches = append(mismatches, Mismatch{
				Kind:         MismatchKindTypeChange,
				Field:        field,
				BaselineType: bType,
				CurrentType:  cType,
			})
		}
	}

	// Fields added in current that were not in baseline.
	for field, cType := range current {
		if _, ok := baseline[field]; !ok {
			mismatches = append(mismatches, Mismatch{
				Kind:        MismatchKindAdded,
				Field:       field,
				CurrentType: cType,
			})
		}
	}

	// Sort for deterministic output.
	sort.Slice(mismatches, func(i, j int) bool {
		if mismatches[i].Field != mismatches[j].Field {
			return mismatches[i].Field < mismatches[j].Field
		}
		return string(mismatches[i].Kind) < string(mismatches[j].Kind)
	})
	return mismatches
}

// copySchema returns a shallow copy of s.
func copySchema(s schema) schema {
	if s == nil {
		return nil
	}
	out := make(schema, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// FormatMismatches produces a multi-line CMD-ready string from a list of
// mismatches.  Returns an empty string if mismatches is empty.
func FormatMismatches(mismatches []Mismatch) string {
	if len(mismatches) == 0 {
		return ""
	}
	lines := make([]string, len(mismatches))
	for i, m := range mismatches {
		lines[i] = m.String()
	}
	return strings.Join(lines, "\n")
}
