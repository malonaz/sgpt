---
title: Built-in tools
description: The ToolService methods the model can call.
---

Declared in `sgpt/v1/tools.proto`. Descriptions and schemas below are the
ones the model sees; they are derived from the proto comments.

## `read_files` — auto

Read the contents of one or more files.

| Argument | Type | Description |
|---|---|---|
| `paths` | string[] | File paths to read. |

Returns one entry per path, in request order, each with content or an
error.

## `read_image` — auto

Look at an image file (PNG, JPEG, GIF or WebP), up to 5 MB.

| Argument | Type | Description |
|---|---|---|
| `path` | string | Image path. |

Returns `ok`; the image follows as a user message, since tool results
cannot carry images.

## `read_document` — auto

Read a PDF, including its layout, tables and figures, up to 10 MB.

| Argument | Type | Description |
|---|---|---|
| `path` | string | PDF path. |

Returns `ok`; the document follows as a user message, titled with its file
name.

## `search_lores` — auto

Search the selected lore libraries with a grep-style regular expression.

| Argument | Type | Description |
|---|---|---|
| `query` | string | Case-insensitive Go/RE2 regex, run over title, description, labels and content. |
| `top_n` | int | Maximum lores to return; default 10. |

Returns matching lores, best first, with title, description and snippets,
and the file path to read for the full text.

## `exec_shell` — review

Execute a shell command on the user's system.

| Argument | Type | Description |
|---|---|---|
| `command` | string | The command. |
| `working_directory` | string | Optional; defaults to the cwd. |

Returns combined stdout/stderr, the exit code, and an error when the
command failed to start or exited non-zero.

## `diff` — review

Edit a file by applying a unified diff. Hunk line numbers are ignored, so
they may be wrong or omitted; hunks must carry enough context to anchor
uniquely and are applied sequentially.

| Argument | Type | Description |
|---|---|---|
| `path` | string | File to edit. |
| `diff` | string | Unified diff. |

Returns the path and the number of hunks applied. The review shows the
resulting diff.

## `replace` — review

Edit a file by applying exact search/replace patches, sequentially.

| Argument | Type | Description |
|---|---|---|
| `path` | string | File to edit. |
| `patches[].search` | string | Exact text; must occur once unless `replace_all`. |
| `patches[].replace` | string | Replacement. |
| `patches[].replace_all` | bool | Replace every occurrence. |

Returns the path and the number of patches applied.

## `agent` group

Five tools enabled together by the name `agent`. See
[Sub-agents](/concepts/agents/) for the model behind them. Results carry
`AgentOperation`s:

| Field | Type | Description |
|---|---|---|
| `name` | string | `operations/{operation}` |
| `agent` | string | The sub-agent, `agents/{agent}` — the handle to message it. |
| `title` | string | The sub-agent's title. |
| `state` | enum | `STATE_QUEUED`, `STATE_RUNNING`, `STATE_AWAITING_REVIEW`, `STATE_SUCCEEDED`, `STATE_FAILED` or `STATE_CANCELLED`. |
| `response` | string | The sub-agent's final message; set once succeeded. |
| `error` | string | Why there is no answer; set once failed or cancelled. |

### `create_agents` — review

Launch one or more sub-agents, each in its own tab, and return at once.

| Argument | Type | Description |
|---|---|---|
| `context` | string | Briefing shared by every task, prepended to each sub-agent's system prompt. |
| `tasks[].title` | string | Tab title. |
| `tasks[].query` | string | The task; the shared context is already in the system prompt. |
| `tasks[].files` | string[] | Paths injected for this task, on top of `files`. |
| `tasks[].model` | string | Model override for this task. |
| `files` | string[] | Paths injected into every sub-agent. |
| `tools` | string[] | Tools granted, narrowing the chat's own; unset inherits them all. The `agent` group is always added. |
| `model` | string | Model for every sub-agent; unset inherits the chat's. |

Returns one operation per task, in request order. Rejected before review
when nesting is at `max_depth`, a task lacks a title or query, or `tools`
names a tool the chat lacks.

### `message_agent` — auto

Message a sub-agent this chat launched, or this chat's own launcher.

| Argument | Type | Description |
|---|---|---|
| `agent` | string | `agents/{agent}`, or `parent` for your launcher. |
| `text` | string | The message. |

Returns the operation resolving to the sub-agent's reply; empty when
messaging `parent`. Auto-executes: the sub-agents were approved at launch.

### `get_operations` — auto

| Argument | Type | Description |
|---|---|---|
| `names` | string[] | Operations to get; empty gets every operation the chat started. |

Returns the operations, in request order (oldest first when listing all).

### `wait_operations` — auto

| Argument | Type | Description |
|---|---|---|
| `names` | string[] | Operations to wait for; empty waits for every unfinished one the chat started. |
| `return_when` | enum | `RETURN_WHEN_ANY` (default: return at the first finish) or `RETURN_WHEN_ALL`. |
| `timeout_seconds` | int | Longest time to block; default 600, capped at 3600. |

Returns a `reason` — `REASON_COMPLETED`, `REASON_TIMEOUT`, or
`REASON_MESSAGE` when a message for the chat arrived first (it follows the
result) — and the waited-for operations. Results a wait collects are not
also delivered as messages.

### `cancel_operations` — auto

| Argument | Type | Description |
|---|---|---|
| `names` | string[] | Operations to cancel. |

Returns the operations, in request order; finished ones are left untouched.
Auto-executes: stopping delegated work only ever saves tokens.
