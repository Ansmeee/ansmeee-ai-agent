package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_AgentConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	// Minimal config without agent section — defaults should apply
	if err := os.WriteFile(cfgPath, []byte("server:\n  port: 9999\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Agent.MaxIterations != 5 {
		t.Errorf("MaxIterations = %d, want 5", cfg.Agent.MaxIterations)
	}
	if cfg.Agent.ToolTimeout != 30*time.Second {
		t.Errorf("ToolTimeout = %v, want 30s", cfg.Agent.ToolTimeout)
	}
	if cfg.Agent.MaxOutputLength != 4096 {
		t.Errorf("MaxOutputLength = %d, want 4096", cfg.Agent.MaxOutputLength)
	}
	if !cfg.Agent.ParallelToolCalls {
		t.Error("ParallelToolCalls should default to true")
	}
	if cfg.Agent.MaxContextMessages != 50 {
		t.Errorf("MaxContextMessages = %d, want 50", cfg.Agent.MaxContextMessages)
	}
}

func TestLoad_AgentConfigExplicit(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  port: 8080
agent:
  max_iterations: 10
  tool_timeout: 60s
  max_output_length: 8192
  parallel_tool_calls: false
  max_context_messages: 100
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Agent.MaxIterations != 10 {
		t.Errorf("MaxIterations = %d, want 10", cfg.Agent.MaxIterations)
	}
	if cfg.Agent.ToolTimeout != 60*time.Second {
		t.Errorf("ToolTimeout = %v, want 60s", cfg.Agent.ToolTimeout)
	}
	if cfg.Agent.MaxOutputLength != 8192 {
		t.Errorf("MaxOutputLength = %d, want 8192", cfg.Agent.MaxOutputLength)
	}
	if cfg.Agent.ParallelToolCalls {
		t.Error("ParallelToolCalls should be false when explicitly set")
	}
	if cfg.Agent.MaxContextMessages != 100 {
		t.Errorf("MaxContextMessages = %d, want 100", cfg.Agent.MaxContextMessages)
	}
}

func TestLoad_MemoryDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	// Only a memory type set; every longterm/task/evolution value falls to defaults.
	if err := os.WriteFile(cfgPath, []byte("memory:\n  type: mysql\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	m := cfg.Memory
	if m.Task.Backend != "memory" {
		t.Errorf("task.backend = %q, want memory", m.Task.Backend)
	}
	if m.Task.IdleTTL != 5*time.Minute {
		t.Errorf("task.idle_ttl = %v, want 5m", m.Task.IdleTTL)
	}
	if !m.LongTerm.Enabled {
		t.Error("longterm.enabled = false, want true (SetDefault)")
	}
	if !m.LongTerm.Router {
		t.Error("longterm.router = false, want true (SetDefault)")
	}
	if !m.LongTerm.Fact.Enabled {
		t.Error("longterm.fact.enabled = false, want true (SetDefault)")
	}
	if m.LongTerm.BudgetRatio != 0.2 {
		t.Errorf("budget_ratio = %v, want 0.2", m.LongTerm.BudgetRatio)
	}
	if m.LongTerm.Fact.TopK != 5 {
		t.Errorf("fact.top_k = %d, want 5", m.LongTerm.Fact.TopK)
	}
	if m.LongTerm.Fact.MinScore != 0.5 {
		t.Errorf("fact.min_score = %v, want 0.5", m.LongTerm.Fact.MinScore)
	}
	if m.LongTerm.Scoring.Confidence != 0.3 || m.LongTerm.Scoring.Freshness != 0.2 || m.LongTerm.Scoring.Relevance != 0.5 {
		t.Errorf("scoring = %+v, want 0.3/0.2/0.5", m.LongTerm.Scoring)
	}
	if m.LongTerm.Admission.WriteThreshold != 0.6 {
		t.Errorf("admission.write_threshold = %v, want 0.6", m.LongTerm.Admission.WriteThreshold)
	}
	if m.LongTerm.Admission.MaxEntriesPerUser != 1000 {
		t.Errorf("admission.max_entries_per_user = %d, want 1000", m.LongTerm.Admission.MaxEntriesPerUser)
	}
	if m.LongTerm.Admission.MaxExtractionsPerSession != 1 {
		t.Errorf("admission.max_extractions_per_session = %d, want 1", m.LongTerm.Admission.MaxExtractionsPerSession)
	}
	if m.LongTerm.Vector.Enabled {
		t.Error("vector.enabled = true, want false")
	}
	if m.Evolution.DecayFactor != 0.95 {
		t.Errorf("evolution.decay_factor = %v, want 0.95", m.Evolution.DecayFactor)
	}
	if m.Evolution.IdleScanCron != "*/1 * * * *" {
		t.Errorf("evolution.idle_scan_cron = %q, want */1 * * * *", m.Evolution.IdleScanCron)
	}
}

func TestLoad_LongTermEnabledExplicitFalse(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	// Explicit false must override SetDefault(true).
	if err := os.WriteFile(cfgPath, []byte("memory:\n  type: mysql\n  longterm:\n    enabled: false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Memory.LongTerm.Enabled {
		t.Error("longterm.enabled = true, want false (explicit YAML must win over SetDefault)")
	}
}
