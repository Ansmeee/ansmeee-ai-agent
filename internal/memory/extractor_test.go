package memory

import (
	"testing"

	"ansmeee-ai-agent/internal/models"
)

func TestExtractor_Human(t *testing.T) {
	x := NewDeterministicExtractor()
	cases := []struct {
		name    string
		content string
		wantKey string
		wantVal string
		wantSrc string
		wantCon float64
		wantCar string
	}{
		{"name_jiao", "你好，我叫张三", "user.name", "张三", SourceRule, 0.9, CardinalitySingle},
		{"name_shi", "我是李四", "user.name", "李四", SourceRule, 0.9, CardinalitySingle},
		{"city", "我在北京工作", "user.city", "北京工作", SourceRule, 0.9, CardinalitySingle},
		{"email", "我的邮箱是zhang@example.com", "user.email", "zhang@example.com", SourceRule, 0.9, CardinalitySingle},
		{"remember", "记住：我喜欢喝咖啡", "user.note", "我喜欢喝咖啡", SourceUserStated, 1.0, CardinalityMulti},
		{"remember_no_colon", "记住 明天要开会", "user.note", "明天要开会", SourceUserStated, 1.0, CardinalityMulti},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := x.Extract("sess1", []Message{{Role: models.RoleHuman, Content: c.content, ID: "m1"}})
			if len(got) != 1 {
				t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
			}
			e := got[0]
			if e.KeyName != c.wantKey || e.Value != c.wantVal {
				t.Fatalf("key/val: got %s=%q want %s=%q", e.KeyName, e.Value, c.wantKey, c.wantVal)
			}
			if e.Source != c.wantSrc || e.Confidence != c.wantCon || e.Cardinality != c.wantCar {
				t.Fatalf("meta: got src=%s conf=%v card=%s", e.Source, e.Confidence, e.Cardinality)
			}
			if e.Channel != ChannelFact {
				t.Fatalf("channel: got %s", e.Channel)
			}
			if !hasEvidence(e.Evidence) {
				t.Fatalf("expected evidence, got %q", e.Evidence)
			}
		})
	}
}

func TestExtractor_InterrogativesNotCaptured(t *testing.T) {
	x := NewDeterministicExtractor()
	// Recall queries share the "我是X"/"我在X"/"我叫X" prefixes; they must not
	// produce (and thus overwrite) profile facts. Regression for user.name="谁".
	queries := []string{
		"我是谁",
		"你还记得我是谁吗",
		"我叫什么名字",
		"我叫啥",
		"我在哪",
		"我在哪个城市",
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			got := x.Extract("s", []Message{{Role: models.RoleHuman, Content: q, ID: "m1"}})
			if len(got) != 0 {
				t.Fatalf("want 0 entries for question %q, got %+v", q, got)
			}
		})
	}
}

func TestExtractor_NoMatch(t *testing.T) {
	x := NewDeterministicExtractor()
	got := x.Extract("s", []Message{{Role: models.RoleHuman, Content: "今天天气怎么样"}})
	if len(got) != 0 {
		t.Fatalf("want 0 entries, got %+v", got)
	}
}

func TestExtractor_ToolCall(t *testing.T) {
	x := NewDeterministicExtractor()
	// mirrors agent.buildToolCallJSON output
	content := `{"tool_calls":[{"id":"c1","name":"weather","arguments":"{\"city\":\"北京\",\"unit\":\"c\"}"}]}`
	got := x.Extract("sess", []Message{{Role: models.RoleFunction, Content: content, ID: "tc1"}})
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	// sorted keys: city, unit
	if got[0].KeyName != "user.mentioned_city" || got[0].Value != "北京" {
		t.Fatalf("first entry: %+v", got[0])
	}
	if got[1].KeyName != "user.mentioned_unit" || got[1].Value != "c" {
		t.Fatalf("second entry: %+v", got[1])
	}
	for _, e := range got {
		if e.Source != SourceRule || e.Confidence != 0.9 || e.Channel != ChannelFact {
			t.Fatalf("meta wrong: %+v", e)
		}
	}
}

func TestExtractor_ToolCall_NonStringArgsSkipped(t *testing.T) {
	x := NewDeterministicExtractor()
	content := `{"tool_calls":[{"id":"c1","name":"calc","arguments":"{\"a\":1,\"b\":\"两\"}"}]}`
	got := x.Extract("s", []Message{{Role: models.RoleFunction, Content: content}})
	if len(got) != 1 || got[0].KeyName != "user.mentioned_b" {
		t.Fatalf("want only string arg b, got %+v", got)
	}
}

func TestExtractor_ToolCall_BadJSON(t *testing.T) {
	x := NewDeterministicExtractor()
	got := x.Extract("s", []Message{{Role: models.RoleFunction, Content: "not json"}})
	if len(got) != 0 {
		t.Fatalf("want 0, got %+v", got)
	}
}
