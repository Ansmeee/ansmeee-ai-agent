package memory

import (
	"context"
	"testing"

	"ansmeee-ai-agent/internal/models"
)

// fakeCompleter returns a canned response and records the prompts it saw.
type fakeCompleter struct {
	resp       string
	err        error
	userPrompt string
	calls      int
}

func (f *fakeCompleter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	f.calls++
	f.userPrompt = userPrompt
	return f.resp, f.err
}

func TestLLMExtractor_ParsesAndMaps(t *testing.T) {
	fc := &fakeCompleter{resp: `以下是结果：
[
  {"kind":"profile","key":"user.name","value":"张三","cardinality":"single","confidence":0.9},
  {"kind":"preference","key":"user.likes","value":"咖啡","cardinality":"multi","confidence":0.8},
  {"kind":"episodic","key":"user.event","value":"下周去北京出差","cardinality":"multi","confidence":0.7}
]`}
	x := NewLLMExtractor(fc, 8)
	got := x.Extract("s1", []Message{{Role: models.RoleHuman, Content: "我叫张三，喜欢咖啡，下周去北京出差", ID: "m1"}})
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(got), got)
	}

	if got[0].Kind != KindProfile || got[0].Cardinality != CardinalitySingle {
		t.Fatalf("profile mapping wrong: %+v", got[0])
	}
	if got[1].Kind != KindPreference || got[1].Cardinality != CardinalityMulti {
		t.Fatalf("preference mapping wrong: %+v", got[1])
	}
	if got[2].Kind != KindEpisodic {
		t.Fatalf("episodic mapping wrong: %+v", got[2])
	}
	for _, e := range got {
		if e.Source != SourceLLMExtracted || e.Channel != ChannelFact {
			t.Fatalf("meta wrong: %+v", e)
		}
		if !hasEvidence(e.Evidence) {
			t.Fatalf("expected evidence, got %q", e.Evidence)
		}
	}
}

func TestLLMExtractor_EmptyArrayAndNoHuman(t *testing.T) {
	fc := &fakeCompleter{resp: "[]"}
	x := NewLLMExtractor(fc, 8)
	if got := x.Extract("s", []Message{{Role: models.RoleHuman, Content: "今天几号", ID: "m1"}}); len(got) != 0 {
		t.Fatalf("want 0 for empty array, got %+v", got)
	}

	// No human messages → no LLM call at all.
	fc2 := &fakeCompleter{resp: "[]"}
	x2 := NewLLMExtractor(fc2, 8)
	x2.Extract("s", []Message{{Role: models.RoleAI, Content: "你好", ID: "a1"}})
	if fc2.calls != 0 {
		t.Fatalf("expected no completer call when no human turns, got %d", fc2.calls)
	}
}

func TestLLMExtractor_InterrogativeValueDropped(t *testing.T) {
	fc := &fakeCompleter{resp: `[{"kind":"profile","key":"user.name","value":"我是谁","cardinality":"single","confidence":0.9}]`}
	x := NewLLMExtractor(fc, 8)
	if got := x.Extract("s", []Message{{Role: models.RoleHuman, Content: "我是谁", ID: "m1"}}); len(got) != 0 {
		t.Fatalf("interrogative value should be dropped, got %+v", got)
	}
}

func TestCompositeExtractor_UnionsBoth(t *testing.T) {
	fc := &fakeCompleter{resp: `[{"kind":"preference","key":"user.likes","value":"咖啡","cardinality":"multi","confidence":0.8}]`}
	comp := NewCompositeExtractor(NewDeterministicExtractor(), NewLLMExtractor(fc, 8))
	got := comp.Extract("s", []Message{{Role: models.RoleHuman, Content: "我叫张三", ID: "m1"}})
	// deterministic → user.name; llm → user.likes
	var haveName, haveLikes bool
	for _, e := range got {
		if e.KeyName == "user.name" {
			haveName = true
		}
		if e.KeyName == "user.likes" {
			haveLikes = true
		}
	}
	if !haveName || !haveLikes {
		t.Fatalf("composite should union both extractors, got %+v", got)
	}
}
