package main

import (
	"flag"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
)

var (
	inlineCodePattern = regexp.MustCompile("`([^`\\n]+)`")
	ariPattern        = regexp.MustCompile(`ari:cloud:[A-Za-z0-9._~:/%<>-]*`)
)

func main() {
	docsDir := flag.String("docs-dir", "docs", "directory containing generated Markdown documentation")
	flag.Parse()

	if err := postprocessDocs(*docsDir); err != nil {
		fmt.Fprintf(os.Stderr, "postprocess docs: %v\n", err)
		os.Exit(1)
	}
}

func postprocessDocs(docsDir string) (returnErr error) {
	root, err := os.OpenRoot(docsDir)
	if err != nil {
		return fmt.Errorf("open docs directory: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close docs directory: %w", err)
		}
	}()

	err = fs.WalkDir(root.FS(), ".", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", filePath, walkErr)
		}
		if entry.IsDir() || path.Ext(filePath) != ".md" {
			return nil
		}

		contents, err := root.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read %s: %w", filePath, err)
		}
		processed := postprocessMarkdown(string(contents))
		if processed == string(contents) {
			return nil
		}

		info, err := root.Stat(filePath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", filePath, err)
		}
		if err := root.WriteFile(filePath, []byte(processed), info.Mode().Perm()); err != nil {
			return fmt.Errorf("write %s: %w", filePath, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk docs directory: %w", err)
	}

	return nil
}

func postprocessMarkdown(markdown string) string {
	lines := strings.SplitAfter(markdown, "\n")
	var output strings.Builder

	for i := 0; i < len(lines); i++ {
		opening := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(opening, "```") {
			output.WriteString(escapeInlineARIs(lines[i]))
			continue
		}

		closingIndex := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				closingIndex = j
				break
			}
		}
		if closingIndex == -1 {
			output.WriteString(lines[i])
			continue
		}

		code := strings.Join(lines[i+1:closingIndex], "")
		if !strings.Contains(code, "ari:cloud:") {
			output.WriteString(strings.Join(lines[i:closingIndex+1], ""))
			i = closingIndex
			continue
		}

		language := strings.TrimSpace(strings.TrimPrefix(opening, "```"))
		// The Registry runs Showdown before replacing emoji shortcodes. The outer
		// block keeps Showdown from escaping these entities as code contents, while
		// retaining the pre/code elements used for highlighting and copy buttons.
		output.WriteString("<div><pre><code")
		if language != "" {
			output.WriteString(` class="language-`)
			output.WriteString(html.EscapeString(language))
			output.WriteString(`"`)
		}
		output.WriteString(">")
		output.WriteString(escapeARIsForHTML(code))
		output.WriteString("</code></pre></div>\n")
		i = closingIndex
	}

	return output.String()
}

func escapeInlineARIs(markdown string) string {
	markdown = inlineCodePattern.ReplaceAllStringFunc(markdown, func(codeSpan string) string {
		if !strings.Contains(codeSpan, "ari:cloud:") {
			return codeSpan
		}

		contents := strings.TrimSuffix(strings.TrimPrefix(codeSpan, "`"), "`")
		return "<samp>" + escapeARIsForHTML(contents) + "</samp>"
	})

	return ariPattern.ReplaceAllStringFunc(markdown, func(ari string) string {
		trimmed := strings.TrimRight(ari, ".")
		trailing := ari[len(trimmed):]
		return "<samp>" + escapeARIsForHTML(trimmed) + "</samp>" + trailing
	})
}

// escapeARIsForHTML preserves the displayed and copied ARI while preventing
// Terraform Registry from treating :cloud: as an emoji shortcode.
func escapeARIsForHTML(value string) string {
	escaped := html.EscapeString(value)
	return strings.ReplaceAll(escaped, "ari:cloud:", "ari&#58;cloud&#58;")
}
