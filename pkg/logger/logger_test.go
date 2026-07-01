package logger

import "testing"

func TestNew_Formats(t *testing.T) {
	for _, format := range []string{"json", "console"} {
		cfg := Config{Level: "info", Format: format, Output: "stdout"}
		l, err := New(cfg)
		if err != nil {
			t.Fatalf("New(%s) error: %v", format, err)
		}
		if l == nil {
			t.Fatalf("New(%s) returned nil logger", format)
		}
	}
}

func TestNew_ValidLevels(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		if _, err := New(Config{Level: level, Format: "json", Output: "stdout"}); err != nil {
			t.Errorf("New(level=%s) unexpected error: %v", level, err)
		}
	}
}

func TestNew_InvalidLevel(t *testing.T) {
	if _, err := New(Config{Level: "bogus", Format: "json", Output: "stdout"}); err == nil {
		t.Error("expected error for invalid level, got nil")
	}
}

func TestL_NonNilBeforeInit(t *testing.T) {
	if L() == nil {
		t.Error("L() returned nil before Init")
	}
}

func TestInit_SwapsGlobal(t *testing.T) {
	before := L()
	if err := Init(Config{Level: "debug", Format: "console", Output: "stdout"}); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	after := L()
	if after == nil {
		t.Fatal("L() nil after Init")
	}
	if before == after {
		t.Error("expected global logger to be swapped by Init")
	}
}

func TestInit_InvalidLevelReturnsError(t *testing.T) {
	if err := Init(Config{Level: "nope", Format: "json", Output: "stdout"}); err == nil {
		t.Error("expected Init to return error for invalid level")
	}
}
