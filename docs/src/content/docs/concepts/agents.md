---
title: Sub-agents
description: Fan self-contained work out to chats of their own.
---

The `agent` tool launches **sub-agents**: one new chat tab per task, each
working from a fresh context until it produces a final answer. The batch's
answers come back together as the tool result of the launching chat.

## When the model uses it

The tool description tells the model to delegate *self-contained* work: a
wide exploration of an unfamiliar area, a mechanical refactor across many
files, several investigations at once. Sub-agents share nothing with the
launching conversation, so everything they need travels in the request.

## Anatomy of a launch

| Argument | Meaning |
|---|---|
| `context` | A briefing written **once** and shared by every task — background, constraints, what to report |
| `tasks[]` | One sub-agent each: a `title` (the tab), a `query`, optional `files` and `model` |
| `files` / `tools` / `model` | Defaults for every task |

The briefing becomes part of each sub-agent's system prompt, after the
role's prompt and a short **contract**: you are a sub-agent, nobody will
answer questions, work to completion, your last message is the report. That
is why a sub-agent finishes with a self-contained answer rather than a
follow-up question.

## What a sub-agent inherits

Everything comes from its launcher, narrowed by the request and never
widened:

- **Tools** — the launcher's enabled tools, or the subset in `tools`. A
  tool the launcher lacks is rejected before anything runs.
- **Model** — the launcher's, unless `model` (or a task's) says otherwise.
- **Permissions** — the launcher's [policy](/concepts/permissions/), live:
  when you "always accept" a tool in the launching chat, its sub-agents
  follow. A sub-agent cannot set its own rules.
- **Depth** — sub-agents may launch their own up to
  `chat.agent.max_depth` levels.

## What you see

Launching is a manual-review call: the request shows the briefing and each
task, the summary line shows the task count, tools, files and model. Once
approved, tabs open **in the background**; the tab bar marks them `●` while
they work.

When a sub-agent needs your verdict, its call **surfaces in the root chat**
— the one you opened — as a pending call tagged `↳ {tab title}`, and you
answer it there exactly like the chat's own: <kbd>ctrl</kbd>+<kbd>j</kbd>
accepts, <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>r</kbd> rejects,
<kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>a</kbd> always accepts the tool for the
chat and every sub-agent under it. Calls are answered oldest first unless you
select one. Nested sub-agents surface in the same root. The sub-agent's tab
still marks `▶` and can answer its own calls; it is an ordinary chat, so you
can also intervene or keep talking to it afterwards.

At most `chat.agent.max_concurrent` sub-agents run at once per nesting
level; the rest of a batch queues. Cancelling the launching turn cancels them all. Sub-agent
chats are labelled `sgpt.com/category: agent` with `sgpt.com/parent-chat`
pointing at the launcher, and stay listed in the menu.
