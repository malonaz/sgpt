---
title: Introduction
description: What SGPT is and what makes it different.
---

SGPT is a command-line AI coding agent. You open a chat in your terminal,
give it a task, and it reads files, runs commands, edits code and searches
your team's knowledge base — pausing for your approval before anything
with side effects.

## What makes it different

**It is configured by the repo, not the user.** Roles, tool sets and lores
live in `.sgpt/` directories committed with the code. Clone a repo and you
get its personas, its integrations and everything the team has learned.

**It talks to one backend.** SGPT is a thin client: chats, messages and
model routing live behind a gRPC `AiService`. The CLI mirrors messages for
display and streams generations; the server persists and bills.

**Tools are protobuf services.** The built-in tools are declared as a gRPC
service whose descriptor is embedded in the binary — descriptions come from
proto comments, JSON schemas from request messages, and auto-execution from
the `idempotency_level`. Remote tool engines expose any other service the
same way.

**Knowledge is a first-class artifact.** Lores are markdown files with YAML
front matter, searched with a regex, and written only with your approval.

## Where to go next

- [Installation](/guides/installation/) — one `go install`.
- [Configuration](/guides/configuration/) — point it at a backend.
- [First chat](/guides/first-chat/) — the TUI in five minutes.
- [How it works](/concepts/architecture/) — the moving parts.
