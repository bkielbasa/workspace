package services

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/renderer/html"
)

var markdownRenderer = goldmark.New(goldmark.WithRendererOptions(html.WithUnsafe()))

func RenderMarkdown(s string) string {
	if s == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := markdownRenderer.Convert([]byte(s), &buf); err != nil {
		return s
	}
	return buf.String()
}
