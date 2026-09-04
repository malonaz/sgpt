---
title: Tools & review
description: What the agent can do, and how you stay in control.
---

Tools are how the model acts. Every tool call goes through the same
pipeline: **review** (compute what to show, decide whether to auto-run),
then **execute**, then the result is sent back as a message.

## Built-in tools

| Tool | Effect | Review |
|---|---|---|
| `read_files` | Read one or more files | auto |
| `search_lores` | Regex search over lore libraries | auto |
| `exec_shell` | Run a shell command | manual |
| `diff` | Apply a unified diff to a file | manual |
| `replace` | Apply exact search/replace patches | manual |
| `agent` | Launch a [sub-agent](/concepts/agents/) | manual |

Auto-execution is not a hardcoded list: a tool auto-runs when its RPC is
declared `idempotency_level = NO_SIDE_EFFECTS`. The same rule applies to
remote tool engines, so a `GetIssue` runs silently while `UpdateIssue`
waits for you.

## What you see

Each tool renders its own request:

- `exec_shell` shows the command as a `sh` code block;
- `diff` and `replace` show a **unified diff computed at review time** — the
  patch has already been applied to a copy and validated, so what you
  approve is exactly what lands;
- engine RPCs show `Service/Method` and the request as JSON.

## Verdicts

When a call awaits review the status line says so and the input box turns
into a reason field:

| Key | Verdict |
|---|---|
| <kbd>alt</kbd>+<kbd>y</kbd> | Accept the call under review |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>y</kbd> | Accept every pending call |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>r</kbd> | Reject; the input text is sent back as the reason |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>a</kbd> | Always accept this tool for the rest of the session |

A rejected call returns an error result carrying your reason, so the model
can adjust rather than retry blindly. Cancelling a turn
(<kbd>ctrl</kbd>+<kbd>c</kbd>) marks every unresolved call as never run.

## Choosing tools

Tools are the union of three sources, deduplicated:

1. `--tool name` on the command line (repeatable);
2. `@tool(...)` directives of the role (and included roles);
3. `chat.default_tools` from the configuration.

Inside a chat <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>t</kbd> opens a fuzzy
picker to toggle any tool — built-in or engine — for the current chat.
