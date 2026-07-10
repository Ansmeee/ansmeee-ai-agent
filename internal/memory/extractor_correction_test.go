package memory

import (
	"testing"

	"ansmeee-ai-agent/internal/models"
)

// Correction phrasings must emit single-cardinality profile entries so that
// Admit archives the prior value and inserts the new one.
func TestExtractor_CorrectionRules(t *testing.T) {
	x := NewDeterministicExtractor()
	cases := []struct {
		name    string
		content string
		wantKey string
		wantVal string
	}{
		{"rename_gaimingjiao", "我改名叫李四", "user.name", "李四"},
		{"rename_xianzaijiao", "我现在叫王五", "user.name", "王五"},
		{"rename_bujiao", "我不叫张三，叫赵六", "user.name", "赵六"},
		{"move_bandao", "我搬到上海", "user.city", "上海"},
		{"move_xianzaizhuzai", "我现在住在深圳", "user.city", "深圳"},
		{"email_change", "我的新邮箱是new@example.com", "user.email", "new@example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := x.Extract("s", []Message{{Role: models.RoleHuman, Content: c.content, ID: "m1"}})
			var found *MemoryEntry
			for i := range got {
				if got[i].KeyName == c.wantKey {
					found = &got[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("no %s entry for %q, got %+v", c.wantKey, c.content, got)
			}
			if found.Value != c.wantVal {
				t.Fatalf("value: got %q want %q", found.Value, c.wantVal)
			}
			if found.Cardinality != CardinalitySingle {
				t.Fatalf("correction must be single-cardinality (archive-then-insert), got %q", found.Cardinality)
			}
			if found.Kind != KindProfile {
				t.Fatalf("kind: got %q want %q", found.Kind, KindProfile)
			}
		})
	}
}
