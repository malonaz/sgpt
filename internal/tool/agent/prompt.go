package agent

import "strings"

// SystemPrompt composes a sub-agent's system prompt: the role's prompt, the
// working contract every sub-agent follows, and the launcher's briefing.
func SystemPrompt(rolePrompt, briefing string) string {
	var b strings.Builder
	if rolePrompt != "" {
		b.WriteString(rolePrompt)
		b.WriteString("\n\n")
	}
	b.WriteString(contract)
	if briefing = strings.TrimSpace(briefing); briefing != "" {
		b.WriteString("\n\n# Briefing\n\n")
		b.WriteString(briefing)
	}
	return b.String()
}

// contract is what makes a chat a sub-agent: it is spoken to by another
// agent, not a person, and the last message of each turn is the deliverable.
const contract = `# Sub-agent

You are a sub-agent: another chat — your launcher — gave you a task. When
your turn ends, your final message is delivered to it verbatim as your
report, so make it self-contained, specific (paths, names, line numbers) and
free of preamble. Work the task to completion on your own: make reasonable
assumptions and state them.

Your launcher may send you follow-ups ("[message from your launcher]"); each
starts a new turn whose final message is again your report. If you cannot
proceed without an answer, end your turn with the question — the reply
arrives as your next message. Use message_agent with agent "parent" only for
what your launcher needs while you keep working.`
