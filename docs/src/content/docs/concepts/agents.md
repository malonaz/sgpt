---
title: Sub-agents
description: Delegate a self-contained task to a chat of its own.
---

The `agent` tool launches a **sub-agent**: a new chat tab that receives a
task, works it to completion with its own tools and context, and returns its
final answer as the tool result of the parent chat.

## When the model uses it

The tool description tells the model to delegate *self-contained* work and
to pass **all** necessary context in the query — the sub-agent shares
nothing with the parent conversation. Typical uses: a wide exploration of
an unfamiliar area, a mechanical refactor across many files, or a parallel
investigation while the parent keeps reasoning.

## What it takes

| Argument | Meaning |
|---|---|
| `query` | The task, with all required context |
| `title` | A few words naming the new tab |
| `files` | Paths to inject into the sub-agent's context |
| `tools` | Built-in tools or tool engine names to grant |
| `model` | Optional model override |

## What you see

A new tab opens with the sub-agent's chat; switch to it
(<kbd>alt</kbd>+<kbd>;</kbd>) to watch or intervene — it is an ordinary
chat, so its tool calls are reviewed the same way. When it produces a final
answer the parent's tool call resolves and the parent turn continues.

Launching a sub-agent is a manual-review tool call: you see the query,
title and grants before it starts.
