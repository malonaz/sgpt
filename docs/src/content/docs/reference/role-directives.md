---
title: Role directives
description: The @directive grammar of .role.md files.
---

A role file is markdown. Lines beginning with `@name(` are directives;
everything else is the prompt body.

## Grammar

**Argument form** — inline, every argument double-quoted:

```
@alias("go")
@label("key", "value")
```

**Text form** — inline or multi-line, terminated by a lone `)`:

```
@summary(one line)
@summary(
free text spanning
several lines
)
```

A line that starts like a directive but does not parse is an **error**. It
is never treated as prose, so a typo cannot silently leak into the prompt.

## Directives

| Directive | Arity | Effect |
|---|---|---|
| `@alias("x")` | 1 | Short name for `-r`. May collide across directories; first wins. |
| `@model("m")` | 1 | Model resource name or alias. Overridden by `-m`. |
| `@file("p")` | 1, repeatable | Inject a path into the context. `dir/...` recurses. Never filtered by `--ext`. |
| `@tool("t")` | 1, repeatable | Built-in tool name or tool set selector. |
| `@role("s")` | 1, repeatable | Include another role by selector. |

## Include semantics

Includes expand depth-first. For each included role, in order: its prompt is
prepended to the including role's prompt, its files and tools are merged
(deduplicated). The result is one flat role; the system prompt template
wraps it once.

## Example

```markdown
@alias("default")
@tool("exec_shell")
@tool("search_lores")
@role("@core//:default")

You are working in acme/billing: the invoicing service. Domains live in
`invoice/`, `ledger/` and `tax/`; generated code under `genproto/` is never
edited by hand.

# Lore index
- `lores/overview` — architecture and where to look.
- `lores/tax/rounding` — why we round half-even and where it bites.
```
