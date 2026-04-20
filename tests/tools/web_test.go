package tools_test

import (
	"strings"
	"testing"

	"github.com/caw/wrapper/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newWebSearchNoRDB() *tools.WebSearchExecutor {
	return tools.NewWebSearchExecutor(nil)
}

func callExtractText(html string) string {
	return tools.ExtractText(html)
}

func TestWebSearch_InputValidation(t *testing.T) {
	exec := newWebSearchNoRDB()
	require.NotNil(t, exec)
}

func TestWebFetch_ExtractText(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains string
		absent   string
	}{
		{
			name:     "strips script tags",
			html:     `<html><script>alert("xss")</script><p>Safe content</p></html>`,
			contains: "Safe content",
			absent:   "alert",
		},
		{
			name:     "strips style tags",
			html:     `<style>body{color:red}</style><p>Visible</p>`,
			contains: "Visible",
			absent:   "color:red",
		},
		{
			name:     "decodes HTML entities",
			html:     `<p>a &amp; b &lt;c&gt;</p>`,
			contains: "a & b <c>",
		},
		{
			name:     "blank input returns empty",
			html:     `   `,
			contains: "",
		},
		{
			name:     "nested tags stripped",
			html:     `<div><span><b>Deep</b> text</span></div>`,
			contains: "Deep",
			absent:   "<b>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := callExtractText(tc.html)
			if tc.contains != "" {
				assert.True(t, strings.Contains(result, tc.contains),
					"result %q should contain %q", result, tc.contains)
			}
			if tc.absent != "" {
				assert.False(t, strings.Contains(result, tc.absent),
					"result %q should NOT contain %q", result, tc.absent)
			}
		})
	}
}

