package organization

import "testing"

func TestMarkdownARI(t *testing.T) {
	t.Parallel()

	got := markdownARI("ari:cloud:jira::site/<site-id>")
	want := "<samp>ari&#58;cloud&#58;jira&#58;&#58;site/&lt;site-id&gt;</samp>"
	if got != want {
		t.Fatalf("markdownARI() = %q, want %q", got, want)
	}
}
