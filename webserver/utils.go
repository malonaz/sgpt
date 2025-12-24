package webserver

import (
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"
)

// formatMessage preserves formatting while making the text HTML-safe
func formatMessageOld(s string) template.HTML {
	// First make the string HTML-safe
	s = template.HTMLEscapeString(s)
	// Replace newlines with <br> tags
	s = strings.ReplaceAll(s, "\n", "<br>")
	// Replace multiple spaces with &nbsp;
	s = strings.ReplaceAll(s, "  ", "&nbsp;&nbsp;")
	return template.HTML(s)
}

func formatMessage(content string) template.HTML {
	// Regular expression to match ```lang content``` blocks
	re := regexp.MustCompile("```([a-zA-Z]+)\n([\\s\\S]+?)```")

	processed := re.ReplaceAllStringFunc(content, func(match string) string {
		parts := re.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}

		language := parts[1]
		code := strings.TrimSpace(parts[2])

		// Create HTML for the code block
		return fmt.Sprintf(`<pre class="line-numbers"><code class="language-%s">%s</code></pre>`,
			html.EscapeString(language),
			html.EscapeString(code))
	})
	// Convert regular text to HTML, preserving line breaks
	return template.HTML(processed)
}
