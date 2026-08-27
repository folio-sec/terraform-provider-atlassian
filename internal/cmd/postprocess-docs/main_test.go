package main

import (
	"strings"
	"testing"
)

func TestPostprocessMarkdownEscapesARIInCodeBlock(t *testing.T) {
	t.Parallel()

	input := "before\n\n```terraform\nresource = \"ari:cloud:jira::site/example\"\n```\n\nafter\n"
	got := postprocessMarkdown(input)

	if strings.Contains(got, ":cloud:") {
		t.Fatalf("postprocessMarkdown() left an emoji shortcode in %q", got)
	}
	want := "before\n\n<div><pre><code class=\"language-terraform\">resource = &#34;ari&#58;cloud&#58;jira::site/example&#34;\n</code></pre></div>\n\nafter\n"
	if got != want {
		t.Fatalf("postprocessMarkdown() = %q, want %q", got, want)
	}
}

func TestPostprocessMarkdownLeavesOtherCodeBlocksUnchanged(t *testing.T) {
	t.Parallel()

	input := "```terraform\nresource = \"example\"\n```\n"
	if got := postprocessMarkdown(input); got != input {
		t.Fatalf("postprocessMarkdown() = %q, want unchanged input", got)
	}
}

func TestPostprocessMarkdownEscapesInlineARIs(t *testing.T) {
	t.Parallel()

	input := "Use ari:cloud: or `ari:cloud:jira::site/<site-id>`.\n"
	want := "Use <samp>ari&#58;cloud&#58;</samp> or <samp>ari&#58;cloud&#58;jira::site/&lt;site-id&gt;</samp>.\n"
	if got := postprocessMarkdown(input); got != want {
		t.Fatalf("postprocessMarkdown() = %q, want %q", got, want)
	}
}

func TestPostprocessMarkdownLeavesOtherInlineCodeUnchanged(t *testing.T) {
	t.Parallel()

	input := "Use `atlassian/org-admin`.\n"
	if got := postprocessMarkdown(input); got != input {
		t.Fatalf("postprocessMarkdown() = %q, want unchanged input", got)
	}
}
