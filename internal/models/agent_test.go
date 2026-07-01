package models

import (
	"encoding/json"
	"testing"
)

func TestJSONStringSlice_RoundTrip(t *testing.T) {
	original := JSONStringSlice{"calculator", "datetime", "weather"}

	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var decoded JSONStringSlice
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("length = %d, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("decoded[%d] = %q, want %q", i, decoded[i], original[i])
		}
	}
}

func TestJSONStringSlice_Nil(t *testing.T) {
	var s JSONStringSlice
	v, err := s.Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != nil {
		t.Errorf("expected nil Value for nil slice, got %v", v)
	}
}

func TestAgent_TemperaturePointerSemantics(t *testing.T) {
	zero := 0.0
	a := Agent{Temperature: &zero}

	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !jsonContains(s, `"temperature":0`) {
		t.Errorf("temperature=0 should be present in JSON, got: %s", s)
	}

	a2 := Agent{}
	b2, _ := json.Marshal(a2)
	s2 := string(b2)
	if jsonContains(s2, `"temperature":null`) {
		// nil pointer serializes to null which is fine
	}
}

func jsonContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
