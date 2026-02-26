package payload_test

import (
	"strings"
	"testing"

	"github.com/firasghr/GoSessionEngine/payload"
)

var baseline = []byte(`{
	"status": "ok",
	"count": 42,
	"items": [1, 2, 3],
	"meta": {
		"page": 1,
		"total": 100
	},
	"active": true,
	"note": null
}`)

func TestLearn_ThenHasBaseline(t *testing.T) {
	v := payload.NewValidator()
	if v.HasBaseline() {
		t.Error("expected no baseline before Learn")
	}
	if err := v.Learn(baseline); err != nil {
		t.Fatalf("Learn error: %v", err)
	}
	if !v.HasBaseline() {
		t.Error("expected baseline after Learn")
	}
}

func TestLearn_InvalidJSON(t *testing.T) {
	v := payload.NewValidator()
	if err := v.Learn([]byte("not json")); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLearn_NonObject(t *testing.T) {
	v := payload.NewValidator()
	if err := v.Learn([]byte(`[1,2,3]`)); err == nil {
		t.Error("expected error for JSON array (non-object)")
	}
}

func TestValidate_NoMismatches(t *testing.T) {
	v := payload.NewValidator()
	if err := v.Learn(baseline); err != nil {
		t.Fatal(err)
	}
	mismatches, err := v.Validate(baseline)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	if len(mismatches) != 0 {
		t.Errorf("expected 0 mismatches, got %d: %v", len(mismatches), mismatches)
	}
}

func TestValidate_MissingField(t *testing.T) {
	v := payload.NewValidator()
	if err := v.Learn(baseline); err != nil {
		t.Fatal(err)
	}

	current := []byte(`{
		"count": 42,
		"items": [1, 2, 3],
		"meta": {"page": 1, "total": 100},
		"active": true,
		"note": null
	}`)
	mismatches, err := v.Validate(current)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}

	found := false
	for _, m := range mismatches {
		if m.Field == "status" && m.Kind == payload.MismatchKindMissing {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MISSING_FIELD for 'status', got: %v", mismatches)
	}
}

func TestValidate_AddedField(t *testing.T) {
	v := payload.NewValidator()
	if err := v.Learn(baseline); err != nil {
		t.Fatal(err)
	}

	current := []byte(`{
		"status": "ok",
		"count": 42,
		"items": [1, 2, 3],
		"meta": {"page": 1, "total": 100},
		"active": true,
		"note": null,
		"new_field": "surprise"
	}`)
	mismatches, err := v.Validate(current)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}

	found := false
	for _, m := range mismatches {
		if m.Field == "new_field" && m.Kind == payload.MismatchKindAdded {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ADDED_FIELD for 'new_field', got: %v", mismatches)
	}
}

func TestValidate_TypeChange(t *testing.T) {
	v := payload.NewValidator()
	if err := v.Learn(baseline); err != nil {
		t.Fatal(err)
	}

	// "count" was a number; now it's a string.
	current := []byte(`{
		"status": "ok",
		"count": "forty-two",
		"items": [1, 2, 3],
		"meta": {"page": 1, "total": 100},
		"active": true,
		"note": null
	}`)
	mismatches, err := v.Validate(current)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}

	found := false
	for _, m := range mismatches {
		if m.Field == "count" && m.Kind == payload.MismatchKindTypeChange {
			if m.BaselineType != "number" || m.CurrentType != "string" {
				t.Errorf("TypeChange baseline=%q current=%q, want number→string", m.BaselineType, m.CurrentType)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("expected TYPE_CHANGE for 'count', got: %v", mismatches)
	}
}

func TestValidate_NestedField(t *testing.T) {
	v := payload.NewValidator()
	if err := v.Learn(baseline); err != nil {
		t.Fatal(err)
	}

	// Remove meta.total.
	current := []byte(`{
		"status": "ok",
		"count": 42,
		"items": [1, 2, 3],
		"meta": {"page": 1},
		"active": true,
		"note": null
	}`)
	mismatches, err := v.Validate(current)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}

	found := false
	for _, m := range mismatches {
		if m.Field == "meta.total" && m.Kind == payload.MismatchKindMissing {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MISSING_FIELD for 'meta.total', got: %v", mismatches)
	}
}

func TestValidate_AutoLearnOnFirstCall(t *testing.T) {
	v := payload.NewValidator()
	// No Learn call; Validate should auto-learn.
	mismatches, err := v.Validate(baseline)
	if err != nil {
		t.Fatalf("Validate error: %v", err)
	}
	if len(mismatches) != 0 {
		t.Errorf("auto-learn should produce 0 mismatches on first call, got %d", len(mismatches))
	}
	if !v.HasBaseline() {
		t.Error("expected baseline to be set after auto-learn")
	}
}

func TestReset(t *testing.T) {
	v := payload.NewValidator()
	if err := v.Learn(baseline); err != nil {
		t.Fatal(err)
	}
	v.Reset()
	if v.HasBaseline() {
		t.Error("expected no baseline after Reset")
	}
}

func TestBaselineFields(t *testing.T) {
	v := payload.NewValidator()
	if err := v.Learn(baseline); err != nil {
		t.Fatal(err)
	}
	fields := v.BaselineFields()
	if len(fields) == 0 {
		t.Error("expected non-empty baseline fields")
	}
	// Fields should be sorted.
	for i := 1; i < len(fields); i++ {
		if fields[i] < fields[i-1] {
			t.Errorf("fields not sorted: %v", fields)
			break
		}
	}
}

func TestFormatMismatches_Empty(t *testing.T) {
	if s := payload.FormatMismatches(nil); s != "" {
		t.Errorf("expected empty string for nil mismatches, got %q", s)
	}
}

func TestFormatMismatches_NonEmpty(t *testing.T) {
	mismatches := []payload.Mismatch{
		{Kind: payload.MismatchKindMissing, Field: "status", BaselineType: "string"},
		{Kind: payload.MismatchKindAdded, Field: "extra", CurrentType: "number"},
	}
	out := payload.FormatMismatches(mismatches)
	if !strings.Contains(out, "PAYLOAD MISMATCH") {
		t.Errorf("expected 'PAYLOAD MISMATCH' in output, got: %q", out)
	}
	if !strings.Contains(out, "status") {
		t.Errorf("expected 'status' in output, got: %q", out)
	}
	if !strings.Contains(out, "extra") {
		t.Errorf("expected 'extra' in output, got: %q", out)
	}
}

func TestMismatch_String(t *testing.T) {
	tests := []struct {
		m    payload.Mismatch
		want string
	}{
		{
			payload.Mismatch{Kind: payload.MismatchKindMissing, Field: "f", BaselineType: "string"},
			"MISSING_FIELD",
		},
		{
			payload.Mismatch{Kind: payload.MismatchKindAdded, Field: "g", CurrentType: "number"},
			"ADDED_FIELD",
		},
		{
			payload.Mismatch{Kind: payload.MismatchKindTypeChange, Field: "h", BaselineType: "number", CurrentType: "string"},
			"TYPE_CHANGE",
		},
	}
	for _, tt := range tests {
		s := tt.m.String()
		if !strings.Contains(s, tt.want) {
			t.Errorf("Mismatch.String() = %q, want it to contain %q", s, tt.want)
		}
	}
}
