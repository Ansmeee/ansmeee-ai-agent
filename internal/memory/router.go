package memory

import "strings"

// QueryClass is the deterministic routing decision for a user message.
type QueryClass int

const (
	ClassDefault  QueryClass = iota // no memory channel needed
	ClassFact                       // fact channel
	ClassContinue                   // TaskState + Policy (Phase 2)
	ClassSemantic                   // Vector + Fact (Phase 3)
)

// QueryFeatures is the router output consumed by MemoryManager.Retrieve.
type QueryFeatures struct {
	Keywords []string
	Class    QueryClass
}

// QueryRouter classifies a user message with zero LLM calls.
type QueryRouter struct{}

// NewQueryRouter builds the router.
func NewQueryRouter() *QueryRouter { return &QueryRouter{} }

// factHints map Chinese question intents to stored key fragments so that
// recall's keyword relevance can hit the corresponding memory_entries.key_name.
var factHints = []struct {
	patterns []string
	keyword  string
}{
	{[]string{"名字", "叫什么", "我是谁", "我叫"}, "name"},
	{[]string{"城市", "在哪", "哪个城市", "地址"}, "city"},
	{[]string{"邮箱", "邮件", "email", "e-mail"}, "email"},
}

// questionMarkers signal a recall-worthy query even without a mapped hint.
var questionMarkers = []string{
	"什么", "谁", "哪", "多少", "几", "吗", "呢", "?", "？",
	"记得", "之前", "上次", "我的",
}

// Route classifies msg and extracts fact keywords.
func (r *QueryRouter) Route(msg string) QueryFeatures {
	lower := strings.ToLower(msg)

	var kws []string
	seen := map[string]struct{}{}
	for _, h := range factHints {
		for _, p := range h.patterns {
			if strings.Contains(lower, strings.ToLower(p)) {
				if _, ok := seen[h.keyword]; !ok {
					seen[h.keyword] = struct{}{}
					kws = append(kws, h.keyword)
				}
				break
			}
		}
	}

	class := ClassDefault
	if len(kws) > 0 {
		class = ClassFact
	} else {
		for _, q := range questionMarkers {
			if strings.Contains(lower, q) {
				class = ClassFact
				break
			}
		}
	}
	return QueryFeatures{Keywords: kws, Class: class}
}
