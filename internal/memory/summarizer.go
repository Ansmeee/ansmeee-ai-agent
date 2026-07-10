package memory

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"ansmeee-ai-agent/pkg/logger"

	"go.uber.org/zap"
)

// summarizeTimeout bounds a single idle-summary LLM call.
const summarizeTimeout = 15 * time.Second

// maxSummaryHistory caps how many recent messages are fed to the summarizer.
const maxSummaryHistory = 40

// Summarizer condenses an idle session into a short summary + topics via one LLM call.
type Summarizer struct {
	completer Completer
	llog      *zap.Logger
}

// NewSummarizer builds the summarizer. Returns nil when completer is nil so the
// manager's nil-guard skips the summary path.
func NewSummarizer(completer Completer) *Summarizer {
	if completer == nil {
		return nil
	}
	return &Summarizer{completer: completer, llog: logger.L()}
}

const summarizeSystemPrompt = `你是一个对话摘要器。将下面的对话浓缩成 JSON：
{"summary":"一段话概括用户的诉求、关键信息与结论","topics":["主题1","主题2"]}
要求：summary 不超过 200 字；topics 为 1-5 个短标签；只输出 JSON。`

// SummaryResult is the parsed summarizer output.
type SummaryResult struct {
	Summary string   `json:"summary"`
	Topics  []string `json:"topics"`
}

// Summarize returns a condensed summary and topic tags for the session history.
func (s *Summarizer) Summarize(ctx context.Context, history []Message) (SummaryResult, error) {
	if s == nil || s.completer == nil {
		return SummaryResult{}, nil
	}
	convo := renderHistory(history)
	if strings.TrimSpace(convo) == "" {
		return SummaryResult{}, nil
	}

	cctx, cancel := context.WithTimeout(ctx, summarizeTimeout)
	defer cancel()

	raw, err := s.completer.Complete(cctx, summarizeSystemPrompt, convo)
	if err != nil {
		return SummaryResult{}, err
	}
	return parseSummary(raw), nil
}

// renderHistory renders the last maxSummaryHistory messages as role-tagged lines.
func renderHistory(history []Message) string {
	if len(history) > maxSummaryHistory {
		history = history[len(history)-maxSummaryHistory:]
	}
	var b strings.Builder
	for _, m := range history {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(content)
		b.WriteString("\n")
	}
	return b.String()
}

// parseSummary tolerates prose/code-fence wrapping around the JSON object.
func parseSummary(raw string) SummaryResult {
	s := strings.TrimSpace(raw)
	if i := strings.IndexByte(s, '{'); i >= 0 {
		if j := strings.LastIndexByte(s, '}'); j >= i {
			s = s[i : j+1]
		}
	}
	var res SummaryResult
	if err := json.Unmarshal([]byte(s), &res); err != nil {
		// fall back to treating the whole response as the summary text
		return SummaryResult{Summary: strings.TrimSpace(raw)}
	}
	return res
}
