package memory

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"ansmeee-ai-agent/internal/models"
)

// Extracted-entry sources.
const (
	SourceRule         = "rule"
	SourceUserStated   = "user_stated"
	SourceLLMExtracted = "llm_extracted"
)

// Entry kinds.
const (
	KindFact       = "fact"
	KindPreference = "preference"
	KindPolicy     = "policy"
	KindSummary    = "summary"
	KindProfile    = "profile"  // identity-type single-valued facts (name/city/email)
	KindEpisodic   = "episodic" // events/episodes; stored via the vector channel
)

// Extractor derives L2 candidate entries from a turn delta. DeterministicExtractor
// (zero LLM) and LLMExtractor both implement it; CompositeExtractor chains them.
type Extractor interface {
	Extract(sessionID string, delta []Message) []MemoryEntry
}

// Rule maps a regex over a human message to a fact key.
type Rule struct {
	re      *regexp.Regexp
	keyName string
	kind    string
}

// DeterministicExtractor derives L2 candidate entries from a turn delta with
// zero LLM calls (regex rules, tool-call args, explicit remember commands).
type DeterministicExtractor struct {
	rules []Rule
}

// NewDeterministicExtractor builds the extractor with the default rule set.
func NewDeterministicExtractor() *DeterministicExtractor {
	return &DeterministicExtractor{rules: defaultRules()}
}

func defaultRules() []Rule {
	return []Rule{
		{re: regexp.MustCompile(`我叫\s*([\p{Han}A-Za-z0-9]{1,20})`), keyName: "user.name", kind: KindProfile},
		{re: regexp.MustCompile(`我是\s*([\p{Han}A-Za-z0-9]{1,20})`), keyName: "user.name", kind: KindProfile},
		{re: regexp.MustCompile(`我在\s*([\p{Han}A-Za-z0-9]{1,20})`), keyName: "user.city", kind: KindProfile},
		{re: regexp.MustCompile(`我的邮箱是\s*([\w.+-]+@[\w.-]+\.[A-Za-z]{2,})`), keyName: "user.email", kind: KindProfile},
		// Correction phrasings. These emit the same single-cardinality profile keys,
		// so Admit's decideAdmit archives the prior active value and inserts the new
		// one (fact_store.go: actionArchiveThenInsert) — no separate archive path.
		{re: regexp.MustCompile(`(?:改名叫|改名为|现在叫|应该叫|不叫.*?叫)\s*([\p{Han}A-Za-z0-9]{1,20})`), keyName: "user.name", kind: KindProfile},
		{re: regexp.MustCompile(`(?:搬到|搬去|现在住在|现在在|改到)\s*([\p{Han}A-Za-z0-9]{1,20})`), keyName: "user.city", kind: KindProfile},
		{re: regexp.MustCompile(`(?:新邮箱是|邮箱改成|邮箱改为)\s*([\w.+-]+@[\w.-]+\.[A-Za-z]{2,})`), keyName: "user.email", kind: KindProfile},
	}
}

var rememberRe = regexp.MustCompile(`^记住[:：,，]?\s*(.+)$`)

// interrogatives are question words that must never be captured as fact values.
// The same "我是X"/"我在X"/"我叫X" rules that store a profile also match recall
// queries like "我是谁"/"我在哪"/"我叫什么" — those turns run through the extractor
// too, so without this guard a question overwrites the single-valued profile key
// (e.g. user.name becomes "谁"). Kept to markers that never occur inside a real
// name or city (几/多少 excluded to avoid clipping e.g. "几内亚").
var interrogatives = []string{
	"谁", "什么", "啥", "哪", "怎么", "怎样", "吗", "呢", "?", "？",
}

// isInterrogativeValue reports whether a captured value looks like a question
// fragment rather than a stated fact.
func isInterrogativeValue(v string) bool {
	for _, q := range interrogatives {
		if strings.Contains(v, q) {
			return true
		}
	}
	return false
}

// tcContent mirrors the RoleFunction message JSON (agent.buildToolCallJSON).
type tcContent struct {
	ToolCalls []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"tool_calls"`
}

// Extract returns candidate entries for the turn. UserID/AgentID are left zero;
// the caller (MemoryManager.OnTurnEnd) stamps them before Admit.
func (x *DeterministicExtractor) Extract(sessionID string, delta []Message) []MemoryEntry {
	var out []MemoryEntry
	for _, m := range delta {
		switch m.Role {
		case models.RoleHuman:
			out = append(out, x.fromHuman(sessionID, m)...)
		case models.RoleFunction:
			out = append(out, x.fromToolCall(sessionID, m)...)
		}
	}
	return out
}

func (x *DeterministicExtractor) fromHuman(sessionID string, m Message) []MemoryEntry {
	content := strings.TrimSpace(m.Content)
	ev := MarshalEvidence([]EvidenceRef{{SessionID: sessionID, MessageID: m.ID}})

	if sub := rememberRe.FindStringSubmatch(content); sub != nil {
		val := strings.TrimSpace(sub[1])
		if val == "" {
			return nil
		}
		return []MemoryEntry{{
			Channel:     ChannelFact,
			Kind:        KindFact,
			KeyName:     "user.note",
			Value:       val,
			Confidence:  1.0,
			Source:      SourceUserStated,
			Cardinality: CardinalityMulti,
			Evidence:    ev,
		}}
	}

	var out []MemoryEntry
	for _, r := range x.rules {
		sub := r.re.FindStringSubmatch(content)
		if sub == nil {
			continue
		}
		val := strings.TrimSpace(sub[1])
		if val == "" || isInterrogativeValue(val) {
			continue
		}
		out = append(out, MemoryEntry{
			Channel:     ChannelFact,
			Kind:        r.kind,
			KeyName:     r.keyName,
			Value:       val,
			Confidence:  0.9,
			Source:      SourceRule,
			Cardinality: CardinalitySingle,
			Evidence:    ev,
		})
	}
	return out
}

func (x *DeterministicExtractor) fromToolCall(sessionID string, m Message) []MemoryEntry {
	var tc tcContent
	if err := json.Unmarshal([]byte(m.Content), &tc); err != nil {
		return nil
	}
	ev := MarshalEvidence([]EvidenceRef{{SessionID: sessionID, MessageID: m.ID}})

	var out []MemoryEntry
	for _, call := range tc.ToolCalls {
		if strings.TrimSpace(call.Arguments) == "" {
			continue
		}
		args := map[string]any{}
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			continue
		}
		keys := make([]string, 0, len(args))
		for k := range args {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic output order
		for _, k := range keys {
			s, ok := args[k].(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, MemoryEntry{
				Channel:     ChannelFact,
				Kind:        KindFact,
				KeyName:     "user.mentioned_" + k,
				Value:       s,
				Confidence:  0.9,
				Source:      SourceRule,
				Cardinality: CardinalityMulti,
				Evidence:    ev,
			})
		}
	}
	return out
}
