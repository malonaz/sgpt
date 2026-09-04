---
title: Lores
description: A committed, searchable knowledge base the agent curates with you.
---

A **lore** is one piece of durable knowledge — a convention, an
architecture note, a gotcha, a runbook — stored as a markdown file under a
repo's `.sgpt/lores/`. Lores are committed with the code, reviewed as prose
in pull requests, and shared across repos through imports.

## A lore file

`.sgpt/lores/style/go.md`:

```markdown
---
title: Go style guide (core)
description: Preferred core libraries — gRPC errors, pbutil, field masks, pagination.
labels:
    lang: go
    topic: style-guide
---

# Style Guide

### Naming Conventions
...
```

The resource name is derived from the path: `lores/style/go`, or
`@core//lores/style/go` from an importing repo. Subdirectories are free —
organise however reads best. Each repo's label vocabulary is persisted in
`.sgpt/labels`.

## Searching

The `search_lores` tool takes a case-insensitive Go regex and runs it
grep-style over every lore's title, description, labels and content across
the selected libraries, returning the best matches with snippets. Prefer
alternations: `go.style|errors`. The result tells the model where the file
is so it can `read_files` the whole thing when a snippet is not enough.

`search_lores` is side-effect free and typically in `chat.default_tools`,
so the agent can consult knowledge in every chat without being asked.

## Always-on lores

```jsonnet
chat: {
  default_lores: ["lores/overview", "@core//lores/style/go"],
}
```

Configured lores are injected into every chat as plain files — selectors
rather than paths, so a lore survives being moved. A repo's `default` role
usually also carries a **lore index**: a short list of every lore and when to
read it.

## The protocol

The system prompt gives the agent four rules:

1. **Search first** before working in an unfamiliar area or guessing a
   convention.
2. Snippets are pointers; read the full file when it matters.
3. Write a lore only for durable, non-obvious, reusable knowledge — never
   for what a file already says, one-off details, or secrets.
4. **Never create, edit or delete a lore on its own initiative.** It
   proposes in a sentence and waits — unless you explicitly ask
   ("write a lore about X", "remember this").

Lores are written solely by the agent, so the prose stays consistent; you
stay the editor.
