package rag

import (
	"fmt"
	"strings"
)

// BuildContextBlock formats results as a [CONTEXT] block for prompt injection.
// Returns empty string if results is empty.
func BuildContextBlock(results []RetrievalResult) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("[CONTEXT]\n")
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, r.Content)
	}
	sb.WriteString("[/CONTEXT]\n")
	return sb.String()
}
