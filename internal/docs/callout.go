// Package docs builds Markdown for Terraform Registry provider documentation.
package docs

import (
	"fmt"
	"strings"
)

// Callout sigils. The Terraform Registry renders a paragraph that starts with
// one of these as a colored callout box.
const (
	// Danger renders a red callout.
	Danger = "!>"
	// Warning renders a yellow callout.
	Warning = "~>"
	// Info renders a blue callout.
	Info = "->"
)

// Callout is a single Registry callout paragraph. Label is emphasized so the
// callout still reads as a warning or a note without its color.
type Callout struct {
	Sigil string
	Label string
	Text  string
}

func (c Callout) String() string {
	// The Registry cannot render a callout that spans paragraphs, so the text is
	// collapsed onto one line and can be wrapped freely at the call site.
	return fmt.Sprintf("%s **%s:** %s", c.Sigil, c.Label, strings.Join(strings.Fields(c.Text), " "))
}

// Description joins a summary sentence with Registry callouts. The summary
// stays the leading paragraph so documentation front matter that takes only
// that paragraph keeps reading as a plain sentence.
func Description(summary string, callouts ...Callout) string {
	paragraphs := make([]string, 0, len(callouts)+1)
	paragraphs = append(paragraphs, strings.Join(strings.Fields(summary), " "))
	for _, callout := range callouts {
		paragraphs = append(paragraphs, callout.String())
	}
	return strings.Join(paragraphs, "\n\n")
}
