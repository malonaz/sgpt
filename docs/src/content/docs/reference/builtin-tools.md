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

## `agent` — review

Launch a sub-agent in a new tab. See [Sub-agents](/concepts/agents/).

| Argument | Type | Description |
|---|---|---|
| `query` | string | The task, with all context — nothing is shared. |
| `title` | string | Tab title. |
| `files` | string[] | Paths to inject. |
| `tools` | string[] | Tools to grant. |
| `model` | string | Optional model override. |

Returns the sub-agent's final answer.
