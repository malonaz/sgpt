---
title: Using lores (the agent knowledge base)
description: How and why to use lore libraries — searching before you work, what belongs in a lore, and the rule to always ask the user before writing or updating one.
labels:
    repo: sgpt
    topic: lores
---

# Using lores

A lore is one piece of durable, hard-won knowledge stored as a markdown file
in a repo at `.sgpt/lores/{id}.md`. Lores are committed with the repo, so
they travel from repo to repo through the configuration's `imports`. They
are the project's memory: what a competent engineer would have to re-derive
by reading the codebase, asking the team, or getting it wrong once.

## Why lores exist

A chat's context dies with the chat. Anything learned the expensive way —
a convention, a gotcha, the reason a design is the way it is — is lost
unless it is written down where the *next* agent will look. Lores exist so
that expensive knowledge is paid for once.

They are deliberately unstructured markdown: the goal is a good explanation
for a reader who lacks all of today's context, not a database record.

## Searching: do it first

Call `search_lores` **before** doing real work in an unfamiliar area, and
before proposing a convention or a library choice. The cost of a search is
a second; the cost of contradicting an established convention is a review
cycle and an inconsistent codebase.

Good moments to search:

- Starting a task in a repo or subsystem you have not touched this session.
- Choosing how to do something that surely has a house style — errors,
  pagination, logging, testing, protos, migrations.
- The user says "like we do elsewhere" or "the usual way".
- You are about to guess.

The query is a **case-insensitive Go/RE2 regular expression**, matched
grep-style over each lore's title, description, labels and content:

- `go.style|aip` — alternation is the workhorse; prefer two or three
  spellings of an idea over one narrow term.
- Metadata hits outrank content hits, so a term from a title or label finds
  the right lore fast.
- Results are snippets, not whole lores. When the snippets look relevant,
  **read the full file** at `{repo}/.sgpt/lores/{id}.md`; `@import//` name
  prefixes identify an imported repo's library (e.g. `@core//lores/go-style`
  lives in the `core` repo's checkout).

A search that returns nothing is a signal too: it often means the knowledge
you are about to acquire is worth capturing.

## What belongs in a lore

Write a lore when knowledge is **durable, non-obvious, and reusable**:

- Conventions and style rules, with the reasoning behind them.
- Architecture and ontology — what the core types mean and how they relate.
- Gotchas and their causes: "X looks like it works, but Y, so do Z".
- Preferred libraries and the idiomatic way to call them.

Do **not** write a lore for:

- Anything derivable by reading a file — a lore that restates code goes
  stale the moment the code changes.
- Task-specific or one-off details; those belong in the conversation.
- Secrets, credentials, or machine-specific paths.

Aim for one coherent topic per lore. Give it a precise `title`, a
`description` that says what a searcher would gain by opening it, and
`labels` reusing the vocabulary already in that repo's `.sgpt/labels` —
reuse keys rather than inventing near-duplicates.

## Always ask before writing or updating

**Never create, edit, or delete a lore on your own initiative.** Lore is
committed to the user's repo and inherited by every future agent, so a
wrong or half-true lore is worse than no lore: it is confidently repeated.
Only the user can judge whether something is settled fact or today's guess.

So: when an opportunity arises, *raise it and wait*.

Opportunities look like this:

- The user corrects you, states a preference, or explains a convention.
- You discover a non-obvious constraint the hard way.
- You notice an existing lore is now wrong, incomplete, or outdated —
  especially after a change you just made.
- A decision gets made in conversation that future work must respect.

The right move is a short, concrete proposal — what you would write, where,
and why — then let the user decide:

> That naming rule isn't in any lore. Want me to add it to
> `@core//lores/go-style`, or write a new `lores/naming` in this repo?

Keep it to a sentence or two; do not interrupt the task with a draft nobody
asked for. If the user says no, drop it and move on — do not re-raise the
same suggestion later in the session.

The one exception is an explicit instruction: when the user says "write a
lore about X" or "remember this", write it without further ceremony.

## File format

YAML front matter for metadata, markdown body for content. Markdown is used
so that diffs review as prose — the body is ordinary markdown, code fences
included.

```markdown
---
title: Short human-readable title
description: One or two lines on what this lore gives a reader.
labels:
    repo: myrepo
    topic: errors
---

# Body

Any markdown.
```

The resource name comes from the file's location and is never written in
the file. Subdirectories become name segments, so
`.sgpt/lores/go/errors.md` is `lores/go/errors` — group related lores into
folders as a library grows.
