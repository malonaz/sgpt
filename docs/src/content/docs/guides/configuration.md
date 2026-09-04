---
title: Configuration
description: The Jsonnet configuration file and how repo overrides layer on top.
---

SGPT reads `~/.config/sgpt/.sgpt.json` (override with `--config`). The file
is [Jsonnet](https://jsonnet.org), evaluated with a native `env()` function
so secrets never touch disk. Unknown fields are errors.

## Minimal configuration

```jsonnet
local env = std.native("env");

{
  grpc_clients: [{
    name: "tsunade",
    base_url: "tsunade.malonaz.com",
    api_key: env("TSUNADE_API_KEY"),
    api_key_header: "tsunade-api-key",
  }],
  ai_service: "tsunade",
  chat: {
    user: "organizations/acme/users/me",
    default_model: "providers/anthropic/models/claude-opus-5",
    summary_model: "providers/google/models/gemini-3.7-flash",
  },
}
```

- `grpc_clients` declares named connections. Every client must resolve to a
  non-empty API key — a missing env variable fails fast.
- `ai_service` names the client serving chats and generations.
- `chat.user` is the resource name chats are created under.
- `chat.summary_model` is the cheap model used to title chats.

## Model aliases

```jsonnet
models: [
  { name: "providers/anthropic/models/claude-opus-5", alias: "opus" },
  { name: "providers/google/models/gemini-3.7-flash",  alias: "fast" },
],
```

`sgpt chat -m fast` resolves through aliases; `@model("fast")` in a role
does too.

## Imports

```jsonnet
imports: [
  { name: "core", path: "~/core" },
],
```

An import makes another repo's roles, tool sets and lores addressable as
`@core//dir:title` and `@core//lores/...`. Imports are scanned lazily and
honour their own ignore rules, never yours.

## Repo overrides

Every `.sgpt.json` found walking up from the current directory is merged on
top of the user file, closest last. A repo typically sets:

```jsonnet
{
  title: "Hearth AI platform: single-binary gRPC services, please, AIP protos.",
  imports: [{ name: "core", path: "~/core" }],
  ignore: ["plz-out/", "genproto/"],
  chat: {
    default_role: "default",
    default_tools: ["search_lores"],
    default_lores: ["lores/overview", "@core//lores/style/go"],
  },
}
```

The nearest `.sgpt.json` also marks the **graph root**: the directory from
which `//dir:title` selectors are resolved.

See the [configuration schema](/reference/configuration/) for every field.
