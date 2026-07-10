package memory

import (
	"context"
	"testing"

	"ansmeee-ai-agent/internal/models"
)

func TestParseSummary_Variants(t *testing.T) {
	// clean JSON
	r := parseSummary(`{"summary":"用户在北京做AI项目","topics":["AI","北京"]}`)
	if r.Summary == "" || len(r.Topics) != 2 {
		t.Fatalf("clean json parse failed: %+v", r)
	}
	// wrapped in prose / code fence
	r2 := parseSummary("```json\n{\"summary\":\"S\",\"topics\":[\"t\"]}\n```")
	if r2.Summary != "S" || len(r2.Topics) != 1 {
		t.Fatalf("fenced json parse failed: %+v", r2)
	}
	// non-JSON falls back to raw text as summary
	r3 := parseSummary("就是一段普通文本")
	if r3.Summary != "就是一段普通文本" {
		t.Fatalf("fallback failed: %+v", r3)
	}
}

func TestSummarizer_Summarize(t *testing.T) {
	fc := &fakeCompleter{resp: `{"summary":"用户咨询了记忆系统设计","topics":["记忆","架构"]}`}
	s := NewSummarizer(fc)
	res, err := s.Summarize(context.Background(), []Message{
		{Role: models.RoleHuman, Content: "帮我设计一个记忆系统"},
		{Role: models.RoleAI, Content: "好的，可以分为L1/L2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary == "" || len(res.Topics) != 2 {
		t.Fatalf("unexpected summary: %+v", res)
	}
	if fc.calls != 1 {
		t.Fatalf("expected 1 completer call, got %d", fc.calls)
	}
}

func TestSummarizer_NilCompleter(t *testing.T) {
	if s := NewSummarizer(nil); s != nil {
		t.Fatal("nil completer should yield nil summarizer for the manager nil-guard")
	}
}

func TestSummarizer_EmptyHistory(t *testing.T) {
	fc := &fakeCompleter{resp: "{}"}
	s := NewSummarizer(fc)
	if _, err := s.Summarize(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if fc.calls != 0 {
		t.Fatalf("empty history must not call the LLM, got %d", fc.calls)
	}
}
