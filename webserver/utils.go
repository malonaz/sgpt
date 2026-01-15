package webserver

import (
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"

	aiv1 "github.com/malonaz/core/genproto/ai/v1"
)

func formatMessage(content string) template.HTML {
	re := regexp.MustCompile("```([a-zA-Z]*)\n([\\s\\S]+?)```")

	processed := re.ReplaceAllStringFunc(content, func(match string) string {
		parts := re.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}

		language := parts[1]
		if language == "" {
			language = "plaintext"
		}
		code := strings.TrimSpace(parts[2])

		return fmt.Sprintf(`<pre class="line-numbers"><code class="language-%s">%s</code></pre>`,
			html.EscapeString(language),
			html.EscapeString(code))
	})
	return template.HTML(processed)
}

func messageRole(role aiv1.Role) string {
	switch role {
	case aiv1.Role_ROLE_USER:
		return "user"
	case aiv1.Role_ROLE_ASSISTANT:
		return "assistant"
	case aiv1.Role_ROLE_SYSTEM:
		return "system"
	default:
		return "unknown"
	}
}
