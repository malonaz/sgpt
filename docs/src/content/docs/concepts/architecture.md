---
title: How it works
description: The moving parts — client, backend, graph, tools, session.
---

SGPT is a thin gRPC client wrapped in a terminal UI. Everything stateful
about a conversation lives server-side; everything about *how you work* lives
in the repo.

```
┌──────────────────────────── terminal ────────────────────────────┐
│  TUI (Bubble Tea)   tabs · timeline · input · menu · pickers     │
│        │                                                         │
│  Session            turn loop · streaming · tool review          │
│        │                     │                                   │
│  Tool registry      builtins · tool engines · sub-agents         │
└────────┼─────────────────────┼───────────────────────────────────┘
         │ AiService (gRPC)    │ AiEngine (gRPC, per tool set)
         ▼                     ▼
   chats · messages       remote services
   models · generation    (jira, your APIs, ...)

   .sgpt/ graph  ─▶  roles · tool sets · lores   (committed with the repo)
```

## The backend

`AiService` owns chats, messages, models and generation. SGPT:

- creates a chat on the first send (quit without typing, nothing is left
  behind);
- mirrors the message history locally for display;
- sends only *new* messages (your text, tool results) with each generation
  request and streams the response;
- lets the server persist, title (via `summary_model`) and account for
  tokens and cost.

Chats are scoped to `chat.user` from the configuration; the menu lists them
all.

## The turn loop

One **turn** is a full exchange: send, stream, and any number of tool-call
rounds until the model produces a final answer. The session is
single-threaded by design — every state change is a blocking call driven
from the UI's command goroutines, and cancelling the turn context aborts it
wherever it is.

Tool calls appear in the stream before the model has finished. The session
resolves them **eagerly**: each call is *reviewed* (metadata attached, a
diff computed, a display header rendered) the moment it appears, so the UI
can show it while the rest streams in. Execution happens after the stream
ends, in order, pausing for your verdict where needed.

## Tools are protobuf services

Built-in tools are declared as a gRPC `ToolService` in `sgpt/v1/tools.proto`
that is never served. Its descriptor set — compiled *with source info* — is
embedded in the binary, and tool definitions are derived from it:

| From the proto | Becomes |
|---|---|
| Method comment | Tool description the model reads |
| Request message | JSON schema for arguments |
| Field comments | Argument descriptions |
| `idempotency_level = NO_SIDE_EFFECTS` | Auto-execute without review |

[Tool engines](/concepts/tool-engines/) apply the same derivation to any
remote service via reflection.

## The graph

Walking up from the cwd, the nearest `.sgpt.json` marks the **graph root**.
From there, every non-ignored directory may hold a `.sgpt/` folder with
roles and tool sets; the root's `.sgpt/lores/` holds the knowledge base.
Configured imports add other repos' graphs under an `@name` prefix. See
[The .sgpt graph](/concepts/graph/).

## The system prompt

Each chat's first message is a `ROLE_SYSTEM` message rendered from a
template: environment facts (OS, shell, cwd, date), house rules
(conciseness, code references), the lore protocol, the tool-discovery
protocol when discoverable engines exist, and finally the role's prompt.
