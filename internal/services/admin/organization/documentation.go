package organization

import (
	"html"
	"strings"
)

// markdownARI renders an ARI as inline sample output without exposing :cloud:
// to Terraform Registry's emoji substitution. The Registry's Markdown parser
// preserves character references inside samp, unlike code spans.
func markdownARI(value string) string {
	escaped := html.EscapeString(value)
	escaped = strings.ReplaceAll(escaped, ":", "&#58;")
	return "<samp>" + escaped + "</samp>"
}
