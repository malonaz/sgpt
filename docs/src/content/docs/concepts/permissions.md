---
title: Permissions
description: Decide up front what runs, what asks, and what never happens.
---

Every tool call ends up in one of three **modes**:

| Mode | Effect |
|---|---|
| `allow` | Runs without asking |
| `review` | Waits for your verdict |
| `deny` | Never runs; the model is told which rule refused it |

With no configuration the mode is the tool's own default: side-effect-free
calls (`read_files`, `search_lores`, engine RPCs declared
`NO_SIDE_EFFECTS`) are allowed, everything else is reviewed.

## Rules

`chat.permissions` in `.sgpt.json` is an ordered list; the first matching
rule decides. A rule names a tool and, optionally, a regular expression an
argument must match — so you can let the read-only half of a tool through
and keep reviewing the rest:

```jsonnet
chat: {
  permissions: [
    // Read-only git and searches run silently.
    { tool: "exec_shell", argument: "command",
      pattern: @"^(git (status|log|diff|show|blame)|rg|ls|cat|wc)",
      mode: "PERMISSION_MODE_ALLOW" },
    // Nothing destructive, ever.
    { tool: "exec_shell", argument: "command",
      pattern: @"(rm -rf|git push --force|DROP TABLE)",
      mode: "PERMISSION_MODE_DENY" },
    // Never touch generated code.
    { tool: "diff", argument: "path", pattern: "^genproto/",
      mode: "PERMISSION_MODE_DENY" },
    { tool: "replace", argument: "path", pattern: "^genproto/",
      mode: "PERMISSION_MODE_DENY" },
  ],
},
```

`argument` is a dotted path into the call's JSON arguments; a repeated
field (`patches.search`) matches when any element does.

## Layers

Policies stack, and the stack only tightens downward:

1. **Your rules** (`chat.permissions`) form the root, applied to every chat.
2. **Grants** — <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>a</kbd> "always accept"
   — allow a tool for the rest of that chat. A grant is your word, so it
   overrides rules.
3. **Sub-agents** get a child policy: they inherit the launcher's decisions
   (grants included, live), and the launch request may add `review` or
   `deny` rules on top — never `allow`.

A denied call resolves immediately as an error result naming the rule, so
the model can route around it instead of retrying.
