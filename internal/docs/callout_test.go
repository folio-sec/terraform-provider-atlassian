package docs

import (
	"strings"
	"testing"
)

func TestDescriptionKeepsSummaryFirstAndCalloutsSingleParagraph(t *testing.T) {
	t.Parallel()

	description := Description(
		"Assigns a role to a user.",
		Callout{Sigil: Warning, Label: "Warning", Text: "Destroying this resource\n\trevokes the role."},
		Callout{Sigil: Info, Label: "Tip", Text: "Import an existing assignment."},
	)

	paragraphs := strings.Split(description, "\n\n")
	want := []string{
		"Assigns a role to a user.",
		"~> **Warning:** Destroying this resource revokes the role.",
		"-> **Tip:** Import an existing assignment.",
	}
	if len(paragraphs) != len(want) {
		t.Fatalf("paragraphs = %#v, want %d", paragraphs, len(want))
	}
	for i, paragraph := range paragraphs {
		if paragraph != want[i] {
			t.Errorf("paragraph %d = %q, want %q", i, paragraph, want[i])
		}
		if strings.Contains(paragraph, "\n") {
			t.Errorf("paragraph %d spans lines, which the Registry cannot render as a callout: %q", i, paragraph)
		}
	}
}

func TestDescriptionWithoutCallouts(t *testing.T) {
	t.Parallel()

	if got := Description("Assigns a role to a user."); got != "Assigns a role to a user." {
		t.Errorf("Description() = %q", got)
	}
}
