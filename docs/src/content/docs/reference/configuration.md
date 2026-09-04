---
title: Configuration schema
description: Every field of .sgpt.json, from sgpt/v1/configuration.proto.
---

The file is Jsonnet; field names are the proto's `snake_case`. Unknown
fields are rejected.

## `Configuration`

| Field | Type | Description |
|---|---|---|
| `grpc_clients` | `GrpcClient[]` | Named gRPC connections. |
| `ai_service` | string | Client name serving chats and generation. |
| `models` | `Model[]` | Model aliases. |
| `chat` | `ChatConfiguration` | Chat defaults. |
| `ignore` | string[] | Gitignore-syntax patterns applied on top of `.gitignore` when walking trees. |
| `title` | string | A few lines about the repo this file belongs to. |
| `imports` | `Import[]` | External repos addressable as `@name//...`. |

## `GrpcClient`

| Field | Type | Description |
|---|---|---|
| `name` | string | Unique identifier, referenced elsewhere. |
| `base_url` | string | Host (`tsunade.malonaz.com`) or URL (`http://localhost:45322`). |
| `api_key` | string | Must be non-empty — use `env("...")`. |
| `api_key_header` | string | Header carrying the key. |

## `Model`

| Field | Type | Description |
|---|---|---|
| `name` | string | `providers/{provider}/models/{model}` |
| `alias` | string | Short name for `-m` and `@model(...)`. |

## `ChatConfiguration`

| Field | Type | Description |
|---|---|---|
| `user` | string | `organizations/{org}/users/{user}` — chats are created and listed under it. |
| `summary_model` | string | Model used to title chats. |
| `default_model` | string | Model when neither `-m` nor the role sets one. |
| `default_role` | string | Role selector or root title used when `-r` is absent. |
| `default_tools` | string[] | Tools advertised in every chat, on top of role and `--tool`. |
| `default_lores` | string[] | Lore selectors injected into every chat (`lores/x`, `@import//lores/x`). |

## `Import`

| Field | Type | Description |
|---|---|---|
| `name` | string | Prefix used in selectors (`@core`). |
| `path` | string | Absolute or `~`-prefixed path to the repo root. |

## Layering

1. `~/.config/sgpt/.sgpt.json` (or `--config`).
2. Every `.sgpt.json` from `/` down to the cwd, closest last, merged with
   proto semantics: scalars replace, lists append.
