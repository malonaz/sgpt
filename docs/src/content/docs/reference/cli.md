---
title: CLI
description: Commands and flags.
---

```
sgpt [--config PATH] <command>
```

`--config` defaults to `~/.config/sgpt/.sgpt.json`. Repo-local `.sgpt.json`
files are merged on top; see [Configuration](/guides/configuration/).

## `sgpt chat [files...]`

Open the TUI. Positional arguments are injected files, same as `--file`.

| Flag | Description |
|---|---|
| `-r, --role` | Role alias or selector (`reviewer`, `//svc:billing`, `@core//:go`). Defaults to `chat.default_role`. |
| `-m, --model` | Model resource name or alias. Defaults to the role's `@model`, then `chat.default_model`. |
| `-f, --file` | Inject a file. `dir` injects one level, `dir/...` recurses. Repeatable. |
| `--ext` | Restrict directory injection to these extensions (`--ext .go --ext .proto`). Role files are never filtered. |
| `--tool` | Enable a built-in tool or tool engine by name. Repeatable. |
| `-c, --continue` | Reopen the most recent chat. |
| `--name` | Reopen a specific chat by resource name. |
| `--max-tokens` | Cap generated tokens. |
| `--temperature` | Sampling temperature, 0.0–2.0. |
| `--debug` | Start a local debug log server for the session. |

Directory injection honours `.gitignore` files and the configuration's
`ignore` patterns.

## `sgpt titles [--dry-run]`

Generate titles, using `chat.summary_model`, for every untitled chat that
has at least one user message. `--dry-run` prints without persisting.

## `sgpt cache`

| Subcommand | Description |
|---|---|
| `cache path` | Print the cache directory. |
| `cache clear` | Delete cached data (resolved engine schemas, input history). |

## `sgpt completion <bash|zsh|fish>`

Print a completion script. Completions are dynamic: roles, models and
aliases, tool names and chats.
