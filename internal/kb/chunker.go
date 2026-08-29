package kb

import (
	"strings"

	"ansmeee-ai-agent/internal/models"
)

// RecursiveChunker 递归切分器：按分隔符层级递归切分，相邻块保留重叠窗口。
// 中文友好：分隔符优先级 "\n\n" → "\n" → "。" → "！" → "？" → " " → 按字符硬切。
type RecursiveChunker struct{}

// NewRecursiveChunker 创建切分器。
func NewRecursiveChunker() *RecursiveChunker { return &RecursiveChunker{} }

var chunkSeparators = []string{"\n\n", "\n", "。", "！", "？", "! ", "? ", " ", ""}

// Chunk 按配置切分纯文本，返回带重叠窗口的分片列表。
func (c *RecursiveChunker) Chunk(plain string, cfg models.KBParseConfig) []string {
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 512
	}
	if cfg.ChunkOverlap < 0 || cfg.ChunkOverlap >= cfg.ChunkSize {
		cfg.ChunkOverlap = cfg.ChunkSize / 4
	}

	plain = strings.TrimSpace(plain)
	if plain == "" {
		return nil
	}
	runes := []rune(plain)
	if len(runes) <= cfg.ChunkSize {
		return []string{plain}
	}

	// 递归切分为句子/段落单元
	units := splitRecursive(runes, cfg.ChunkSize, chunkSeparators)

	// 合并单元为目标大小的分片，带重叠
	var chunks []string
	for i := 0; i < len(units); {
		var buf strings.Builder
		start := i
		for i < len(units) && buf.Len()+runeLen(units[i]) <= cfg.ChunkSize {
			buf.WriteString(units[i])
			i++
		}
		if buf.Len() == 0 {
			// 单个 unit 超过 chunkSize，硬切
			hard := hardSplit([]rune(units[start]), cfg.ChunkSize)
			chunks = append(chunks, hard...)
			i = start + 1
			continue
		}
		chunks = append(chunks, strings.TrimSpace(buf.String()))

		// 重叠：回退若干 unit 形成重叠窗口
		if cfg.ChunkOverlap > 0 && i < len(units) {
			back := backtrack(units, start, i, cfg.ChunkOverlap)
			i = back
		}
	}
	return chunks
}

// splitRecursive 按 separators 层级递归切分，保证每个 unit <= maxSize（rune）。
func splitRecursive(runes []rune, maxSize int, seps []string) []string {
	if len(runes) <= maxSize {
		return []string{string(runes)}
	}
	if len(seps) == 0 {
		return hardSplit(runes, maxSize)
	}
	sep := seps[0]
	parts := strings.Split(string(runes), sep)
	var result []string
	for _, p := range parts {
		pr := []rune(p)
		if len(pr) <= maxSize {
			if strings.TrimSpace(p) != "" {
				result = append(result, p+sepTail(p, sep))
			}
		} else {
			result = append(result, splitRecursive(pr, maxSize, seps[1:])...)
		}
	}
	return result
}

// sepTail 如果原文以分隔符结尾，切分后补回（保持句子完整性）。
func sepTail(s, sep string) string {
	if strings.HasSuffix(s, sep) {
		return ""
	}
	return sep
}

// hardSplit 按 rune 硬切分。
func hardSplit(runes []rune, size int) []string {
	var out []string
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// runeLen 返回字符串的 rune 数。
func runeLen(s string) int { return len([]rune(s)) }

// backtrack 从 start 往后找到累计 rune 数 >= overlap 的回退点。
func backtrack(units []string, start, cur, overlap int) int {
	acc := 0
	for j := cur - 1; j > start; j-- {
		acc += runeLen(units[j])
		if acc >= overlap {
			return j
		}
	}
	return cur
}
