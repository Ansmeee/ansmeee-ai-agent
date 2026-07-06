package memory

import (
	"testing"
)

func TestRouter_Route(t *testing.T) {
	r := NewQueryRouter()
	cases := []struct {
		name      string
		msg       string
		wantClass QueryClass
		wantKw    []string
	}{
		{"name_q", "我叫什么名字", ClassFact, []string{"name"}},
		{"city_q", "我在哪个城市", ClassFact, []string{"city"}},
		{"email_q", "我的邮箱是多少", ClassFact, []string{"email"}},
		{"generic_q", "今天几号", ClassFact, nil},
		{"memory_word", "我之前说过的", ClassFact, nil},
		{"statement", "帮我写一段代码", ClassDefault, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Route(c.msg)
			if got.Class != c.wantClass {
				t.Fatalf("class: got %d want %d", got.Class, c.wantClass)
			}
			if !kwEqual(got.Keywords, c.wantKw) {
				t.Fatalf("keywords: got %v want %v", got.Keywords, c.wantKw)
			}
		})
	}
}

func kwEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
