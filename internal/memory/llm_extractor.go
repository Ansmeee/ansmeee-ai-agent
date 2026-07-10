package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"ansmeee-ai-agent/internal/llm"
	"ansmeee-ai-agent/internal/models"
	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
)

// llmExtractTimeout bounds a single LLM extraction call on the async write path.
const llmExtractTimeout = 8 * time.Second

// Completer is the narrow single-shot LLM interface the extractor and summarizer
// need. Kept local to the memory package so both are testable with a fake and the
// package stays decoupled from the concrete provider.
type Completer interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// providerCompleter adapts *llm.Provider to Completer, optionally overriding the
// model (e.g. cfg.LongTerm.ExtractionModel) via a per-call provider copy.
type providerCompleter struct {
	p *llm.Provider
}

// NewProviderCompleter wraps a provider for single-shot completions. When model
// is non-empty the provider is cloned with that model override; on clone failure
// it falls back to the base provider.
func NewProviderCompleter(p *llm.Provider, model string) Completer {
	if p == nil {
		return nil
	}
	if model != "" {
		if op, err := p.WithOverride(model, "", ""); err == nil {
			p = op
		} else {
			logger.L().Warn("completer model override failed, using base model",
				zap.String("model", model), zap.Error(err))
		}
	}
	return &providerCompleter{p: p}
}

func (c *providerCompleter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	msgs := []llm.MessageContent{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userPrompt},
	}
	res, err := c.p.Chat(ctx, msgs, nil, llm.WithTemperature(0))
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// CompositeExtractor runs the deterministic extractor first (high precision, zero
// cost) then the LLM extractor (broad coverage). Downstream Admit dedups the union.
type CompositeExtractor struct {
	deterministic Extractor
	llm           *LLMExtractor
}

// NewCompositeExtractor chains a deterministic and an LLM extractor. Either may be
// nil; a nil LLM extractor degrades to deterministic-only.
func NewCompositeExtractor(deterministic Extractor, llm *LLMExtractor) *CompositeExtractor {
	return &CompositeExtractor{deterministic: deterministic, llm: llm}
}

// Extract returns the union of deterministic and LLM candidate entries.
func (c *CompositeExtractor) Extract(sessionID string, delta []Message) []MemoryEntry {
	var out []MemoryEntry
	if c.deterministic != nil {
		out = append(out, c.deterministic.Extract(sessionID, delta)...)
	}
	if c.llm != nil {
		out = append(out, c.llm.Extract(sessionID, delta)...)
	}
	return out
}

// LLMExtractor derives candidate entries via a single structured LLM call.
type LLMExtractor struct {
	completer Completer
	maxItems  int
	llog      *zap.Logger
}

// NewLLMExtractor builds the LLM-backed extractor. maxItems <= 0 defaults to 8.
func NewLLMExtractor(completer Completer, maxItems int) *LLMExtractor {
	if maxItems <= 0 {
		maxItems = 8
	}
	return &LLMExtractor{completer: completer, maxItems: maxItems, llog: logger.L()}
}

const extractSystemPrompt = `你是一个记忆抽取器。从对话中抽取值得长期记住的用户信息。
只输出 JSON 数组，每个元素形如：
{"kind":"profile|preference|episodic","key":"user.xxx","value":"...","cardinality":"single|multi","confidence":0.0-1.0}
规则：
- profile：身份型稳定事实（姓名/城市/邮箱/职业），cardinality=single。
- preference：偏好/喜好/习惯，cardinality=multi。
- episodic：具体事件/情节/计划，cardinality=multi。
- key 使用点分小写英文，如 user.name、user.city、user.likes、user.event。
- 只抽取用户明确陈述的信息，不要臆测；无可抽取时输出 []。
- 不要输出问题、疑问句或助手自己的话。`

// extractItem mirrors one element of the model's JSON array output.
type extractItem struct {
	Kind        string  `json:"kind"`
	Key         string  `json:"key"`
	Value       string  `json:"value"`
	Cardinality string  `json:"cardinality"`
	Confidence  float64 `json:"confidence"`
}

// Extract runs the LLM over the human turns in delta and maps the JSON result to
// MemoryEntry candidates. Failures return nil (deterministic path still applies).
func (x *LLMExtractor) Extract(sessionID string, delta []Message) []MemoryEntry {
	if x == nil || x.completer == nil {
		return nil
	}
	convo, evrefs := x.gatherHuman(sessionID, delta)
	if strings.TrimSpace(convo) == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), llmExtractTimeout)
	defer cancel()

	raw, err := x.completer.Complete(ctx, extractSystemPrompt, convo)
	if err != nil {
		x.llog.Warn("llm extract call failed", zap.Error(err))
		return nil
	}
	items, err := parseExtractItems(raw)
	if err != nil {
		x.llog.Warn("llm extract parse failed", zap.String("raw", raw), zap.Error(err))
		return nil
	}

	ev := MarshalEvidence(evrefs)
	out := make([]MemoryEntry, 0, len(items))
	for _, it := range items {
		e, ok := it.toEntry(ev)
		if !ok {
			continue
		}
		out = append(out, e)
		if len(out) >= x.maxItems {
			break
		}
	}
	return out
}

func (x *LLMExtractor) gatherHuman(sessionID string, delta []Message) (string, []EvidenceRef) {
	var b strings.Builder
	var refs []EvidenceRef
	for _, m := range delta {
		if m.Role != models.RoleHuman {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		b.WriteString("用户: ")
		b.WriteString(content)
		b.WriteString("\n")
		refs = append(refs, EvidenceRef{SessionID: sessionID, MessageID: m.ID})
	}
	return b.String(), refs
}

// parseExtractItems tolerates models that wrap the array in prose or code fences.
func parseExtractItems(raw string) ([]extractItem, error) {
	s := strings.TrimSpace(raw)
	if i := strings.IndexByte(s, '['); i >= 0 {
		if j := strings.LastIndexByte(s, ']'); j >= i {
			s = s[i : j+1]
		}
	}
	var items []extractItem
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (it extractItem) toEntry(evidence string) (MemoryEntry, bool) {
	key := strings.TrimSpace(it.Key)
	val := strings.TrimSpace(it.Value)
	if key == "" || val == "" || isInterrogativeValue(val) {
		return MemoryEntry{}, false
	}

	kind := KindPreference
	cardinality := CardinalityMulti
	switch strings.ToLower(it.Kind) {
	case KindProfile:
		kind, cardinality = KindProfile, CardinalitySingle
	case KindEpisodic:
		kind, cardinality = KindEpisodic, CardinalityMulti
	case KindPreference:
		kind, cardinality = KindPreference, CardinalityMulti
	}
	if strings.EqualFold(it.Cardinality, CardinalitySingle) {
		cardinality = CardinalitySingle
	}

	conf := it.Confidence
	if conf <= 0 || conf > 1 {
		conf = 0.7
	}

	return MemoryEntry{
		Channel:     ChannelFact,
		Kind:        kind,
		KeyName:     key,
		Value:       val,
		Confidence:  conf,
		Source:      SourceLLMExtracted,
		Cardinality: cardinality,
		Evidence:    evidence,
	}, true
}
