---
title: Roles
description: Personas as markdown files, composable and importable.
---

A role is a persona: a system prompt plus the model, files and tools it
should start with. Roles are `.sgpt/{title}.role.md` files.

```markdown
@alias("review")
@model("opus")
@tool("exec_shell")
@tool("@core//:jira")
@file("CONTRIBUTING.md")
@role("@core//:default")

You are a meticulous reviewer. Prefer small, behaviour-preserving
suggestions; cite `file:line` for everything you flag.
```

Lines starting with `@name(...)` are **directives**; everything else is the
prompt body. Anything that looks like a directive but is malformed is an
error — never silently folded into the prompt.

## Directives

| Directive | Meaning |
|---|---|
| `@alias("x")` | Short name usable in `-r x` |
| `@model("...")` | Model resource name or alias; overridden by `-m` |
| `@file("path")` | Inject a file (or `dir/...`) into the context |
| `@tool("name")` | Enable a built-in tool or a tool set selector |
| `@role("//dir:title")` | Include another role |

Directives come in two shapes: argument form with double-quoted arguments
(`@label("key", "value")`), and text form which may span lines and ends with
a lone `)`:

```
@summary(
free text...
)
```

## Composition

`@role(...)` includes are expanded depth-first: the included role's prompt is
**prepended** and its files and tools merged. A repo's `default` role
typically includes `@core//:default` to inherit organisation-wide rules and
then adds the repo's own context and lore index.

## Selection

```bash
sgpt chat                 # chat.default_role from the nearest .sgpt.json
sgpt chat -r reviewer     # alias or root title
sgpt chat -r //svc:billing
sgpt chat -r @core//:go
```

Completion lists every role in the graph and its imports.
