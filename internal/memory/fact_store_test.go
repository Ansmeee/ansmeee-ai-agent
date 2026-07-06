package memory

import (
	"testing"
	"time"

	"ansmeee-ai-agent/internal/config"
)

func evidenceJSON() string {
	return MarshalEvidence([]EvidenceRef{{SessionID: "s1", MessageID: "m1"}})
}

func TestPassesGate(t *testing.T) {
	ac := config.AdmissionConfig{WriteThreshold: 0.6}
	tests := []struct {
		name string
		e    MemoryEntry
		want bool
	}{
		{"no evidence rejected", MemoryEntry{Confidence: 1.0, Evidence: ""}, false},
		{"empty array evidence rejected", MemoryEntry{Confidence: 1.0, Evidence: "[]"}, false},
		{"low confidence rejected", MemoryEntry{Confidence: 0.5, Evidence: evidenceJSON()}, false},
		{"at threshold with evidence admitted", MemoryEntry{Confidence: 0.6, Evidence: evidenceJSON()}, true},
		{"high confidence admitted", MemoryEntry{Confidence: 0.9, Evidence: evidenceJSON()}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := passesGate(tt.e, ac); got != tt.want {
				t.Errorf("passesGate = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecideAdmit(t *testing.T) {
	t.Run("exact value_hash present → skip", func(t *testing.T) {
		incoming := MemoryEntry{KeyName: "user.city", ValueHash: ValueHash("北京"), Cardinality: CardinalityMulti}
		existing := []MemoryEntry{{ID: 1, KeyName: "user.city", ValueHash: ValueHash("北京")}}
		action, ids := decideAdmit(incoming, existing)
		if action != actionSkip || ids != nil {
			t.Errorf("action = %v, ids = %v; want skip/nil", action, ids)
		}
	})

	t.Run("single-valued new value → archive old then insert", func(t *testing.T) {
		incoming := MemoryEntry{KeyName: "user.current_city", ValueHash: ValueHash("上海"), Cardinality: CardinalitySingle}
		existing := []MemoryEntry{{ID: 7, KeyName: "user.current_city", ValueHash: ValueHash("北京")}}
		action, ids := decideAdmit(incoming, existing)
		if action != actionArchiveThenInsert {
			t.Fatalf("action = %v, want archiveThenInsert", action)
		}
		if len(ids) != 1 || ids[0] != 7 {
			t.Errorf("archive ids = %v, want [7]", ids)
		}
	})

	t.Run("multi-valued new value → insert coexist", func(t *testing.T) {
		incoming := MemoryEntry{KeyName: "user.visited_city", ValueHash: ValueHash("广州"), Cardinality: CardinalityMulti}
		existing := []MemoryEntry{{ID: 3, KeyName: "user.visited_city", ValueHash: ValueHash("北京")}}
		action, ids := decideAdmit(incoming, existing)
		if action != actionInsert || ids != nil {
			t.Errorf("action = %v, ids = %v; want insert/nil", action, ids)
		}
	})

	t.Run("single-valued no existing → insert", func(t *testing.T) {
		incoming := MemoryEntry{KeyName: "user.current_city", ValueHash: ValueHash("北京"), Cardinality: CardinalitySingle}
		action, _ := decideAdmit(incoming, nil)
		if action != actionInsert {
			t.Errorf("action = %v, want insert", action)
		}
	})
}

func TestFreshnessOf(t *testing.T) {
	now := time.Now()
	fresh := MemoryEntry{CreatedAt: now}
	if f := freshnessOf(fresh, 0.95, now); f < 0.999 {
		t.Errorf("fresh entry freshness = %v, want ~1.0", f)
	}
	old := MemoryEntry{CreatedAt: now.Add(-10 * 24 * time.Hour)}
	f := freshnessOf(old, 0.95, now)
	if f >= 1.0 || f <= 0 {
		t.Errorf("10-day-old freshness = %v, want in (0,1)", f)
	}
	// last_used_at overrides created_at.
	used := now.Add(-1 * 24 * time.Hour)
	e := MemoryEntry{CreatedAt: now.Add(-100 * 24 * time.Hour), LastUsedAt: &used}
	if f := freshnessOf(e, 0.95, now); f < freshnessOf(old, 0.95, now) {
		t.Errorf("last_used_at should make entry fresher than 10-day-old by created_at")
	}
}

func TestRelevanceOf(t *testing.T) {
	e := MemoryEntry{KeyName: "user.city", Value: "北京", Kind: "fact"}
	if r := relevanceOf(e, RecallQuery{Keywords: []string{"user.city"}}); r != 1.0 {
		t.Errorf("exact key hit = %v, want 1.0", r)
	}
	if r := relevanceOf(e, RecallQuery{Keywords: []string{"city"}}); r != 0.5 {
		t.Errorf("partial key hit = %v, want 0.5", r)
	}
	if r := relevanceOf(e, RecallQuery{Keywords: []string{"北京"}}); r != 0.5 {
		t.Errorf("value hit = %v, want 0.5", r)
	}
	if r := relevanceOf(e, RecallQuery{Kinds: []string{"fact"}}); r != 0.5 {
		t.Errorf("kind hit = %v, want 0.5", r)
	}
	if r := relevanceOf(e, RecallQuery{Keywords: []string{"weather"}}); r != 0 {
		t.Errorf("no hit = %v, want 0", r)
	}
}

func TestScoreOfAndSort(t *testing.T) {
	now := time.Now()
	w := config.ScoreWeights{Confidence: 0.3, Freshness: 0.2, Relevance: 0.5}
	q := RecallQuery{Keywords: []string{"user.city"}}
	hi := MemoryEntry{KeyName: "user.city", Confidence: 1.0, CreatedAt: now}
	lo := MemoryEntry{KeyName: "user.other", Confidence: 0.6, CreatedAt: now.Add(-30 * 24 * time.Hour)}
	shi := scoreOf(hi, q, w, 0.95, now)
	slo := scoreOf(lo, q, w, 0.95, now)
	if shi <= slo {
		t.Errorf("expected hi (%.3f) > lo (%.3f)", shi, slo)
	}

	list := []ScoredEntry{{Score: 0.2}, {Score: 0.9}, {Score: 0.5}}
	sortScoredDesc(list)
	if list[0].Score != 0.9 || list[1].Score != 0.5 || list[2].Score != 0.2 {
		t.Errorf("sort order wrong: %+v", list)
	}
}

func TestHasEvidence(t *testing.T) {
	cases := map[string]bool{
		"":                      false,
		"[]":                    false,
		"null":                  false,
		`[{"session_id":"s1"}]`: true,
		evidenceJSON():          true,
	}
	for in, want := range cases {
		if got := hasEvidence(in); got != want {
			t.Errorf("hasEvidence(%q) = %v, want %v", in, got, want)
		}
	}
}
