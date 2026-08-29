package kb

import (
	"context"
	"io"
	"regexp"
	"strings"

	"ansmeee-ai-agent/internal/models"
)

// docParser 支持 Markdown / 纯文本 / URL（HTML strip）解析。
type docParser struct{}

// NewDocParser 创建解析器。
func NewDocParser() DocParser { return &docParser{} }

var (
	htmlTagRE   = regexp.MustCompile(`<[^>]*>`)
	htmlScript  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	htmlStyle   = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	multiSpace  = regexp.MustCompile(`[ \t]+`)
	multiBreak  = regexp.MustCompile(`\n{3,}`)
	mdCodeFence = regexp.MustCompile("(?s)```.*?```")
)

// Parse 从 Reader 读取并解析为纯文本。
func (p *docParser) Parse(_ context.Context, r io.Reader, sourceType string) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	switch sourceType {
	case models.SourceTypeMarkdown:
		return p.parseMarkdown(string(raw)), nil
	case models.SourceTypeURL:
		return p.parseHTML(string(raw)), nil
	default: // text / upload
		return strings.TrimSpace(string(raw)), nil
	}
}

// parseMarkdown 保留代码块，strip HTML 标签，归一化空白。
func (p *docParser) parseMarkdown(s string) string {
	// 保留代码块内容（先提取占位，最后还原）
	codes := mdCodeFence.FindAllString(s, -1)
	for i, c := range codes {
		s = strings.Replace(s, c, "\x00CODE"+strings.Repeat("X", i)+"\x00", 1)
	}
	s = htmlTagRE.ReplaceAllString(s, "")
	s = multiSpace.ReplaceAllString(s, " ")
	s = multiBreak.ReplaceAllString(s, "\n\n")
	for i, c := range codes {
		s = strings.Replace(s, "\x00CODE"+strings.Repeat("X", i)+"\x00", c, 1)
	}
	return strings.TrimSpace(s)
}

// parseHTML 去除 script/style/标签，保留文本结构。
func (p *docParser) parseHTML(s string) string {
	s = htmlScript.ReplaceAllString(s, "")
	s = htmlStyle.ReplaceAllString(s, "")
	s = htmlTagRE.ReplaceAllString(s, "")
	s = multiSpace.ReplaceAllString(s, " ")
	s = multiBreak.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
