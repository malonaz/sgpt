---
title: The .sgpt graph
description: Where artifacts live, how they are addressed, how imports work.
---

The graph is the set of `.sgpt/` artifacts reachable from a repo, addressed
with please-style selectors.

## Layout

```
my-repo/
├── .sgpt.json                 ← graph root + repo config override
├── .sgpt/
│   ├── default.role.md        //:default
│   ├── reviewer.role.md       //:reviewer
│   ├── jira.toolset           //:jira
│   ├── labels                 ← lore label vocabulary
│   └── lores/
│       ├── overview.md        lores/overview
│       └── style/go.md        lores/style/go
└── services/billing/
    └── .sgpt/
        └── billing.role.md    //services/billing:billing
```

| Artifact | Extension | Content |
|---|---|---|
| [Role](/concepts/roles/) | `.role.md` | `@directive(...)` lines + markdown prompt |
| [Tool set](/concepts/tool-engines/) | `.toolset` | JSON: engine client + service tool sets |
| [Lore](/concepts/lores/) | `lores/*.md` | YAML front matter + markdown body |

## Selectors

```
//dir:title          artifact in dir, relative to the graph root
//:title   or  title artifact at the root
//dir                every artifact in dir (completion, listing)
@core//dir:title     same, in the imported repo named "core"
```

Roles may also declare an `@alias("go")`; aliases are a convenience and may
collide across directories — the first wins. Selectors never collide.

## Discovery

The tree is scanned breadth-first from the root, honouring `.gitignore`
files and the configuration's extra `ignore` patterns. Scanning is cheap and
happens on every invocation, so completion and role resolution are always
current. Imports are scanned lazily — only when a selector targets them or
completion lists everything — and with **their own** ignore rules.

## Configuration layering

Every `.sgpt.json` on the path from the cwd to `/` is merged over the user
configuration, closest last. Repos use this to set a `title`, `imports`,
`ignore`, `chat.default_role`, `chat.default_tools` and
`chat.default_lores`. See [Configuration](/guides/configuration/).
