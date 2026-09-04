---
title: Tool engines
description: Turn any gRPC service into a set of tools.
---

A **tool set** file points at a remote gRPC service and lists the methods
the model may call. SGPT resolves the service's schema over reflection and
turns each method into a tool — the same derivation used for the built-ins:
comments become descriptions, request messages become JSON schemas,
`NO_SIDE_EFFECTS` methods auto-execute.

## A tool set file

`.sgpt/jira.toolset`:

```json
{
  "engine_service": "onikisu-staging",
  "tool_sets": [
    {
      "service_full_name": "gateway.integration.jira.v1.JiraGateway",
      "method_names": [
        "GetIssue", "ListIssues", "CreateIssue", "UpdateIssue",
        "ListSprints", "StartSprint", "CloseSprint"
      ],
      "schema_configuration": {
        "with_max_depth": 8,
        "with_response_read_mask": true,
        "with_response_schema_max_depth": 2
      }
    }
  ]
}
```

- `engine_service` names a `grpc_clients` entry from the configuration —
  the engine that serves the `AiEngine` API and proxies calls.
- Each entry in `tool_sets` is a `CreateServiceToolSetRequest`: a service,
  the methods to expose, and schema tuning (depth caps keep argument
  schemas small enough for the model).

The file's selector is its tool name: `--tool jira`, `--tool //:jira`,
`@tool("@onikisu//:jira")`.

## Lazy by design

Engines are **listed** at startup (so pickers and completion know them) but
**dialed only when first enabled** — on the command line, by a role, or
from the picker. Schema resolution and tool set creation happen once per
engine per process and are cached.

## Discovery

Large services can be exposed as **discoverable**: instead of advertising
every method up front, the engine advertises a `discover` tool. The system
prompt then teaches the protocol — discover before calling, discover
everything you need in one turn — and the tool sets are attached when the
model asks. Discovery calls are auto-resolved at review time; nothing runs.

## Rendering

RPC calls render as `Service/Method` with the request as JSON; results
render as JSON with the response read mask applied. Big responses are
collapsible in the timeline (<kbd>alt</kbd>+<kbd>z</kbd>).
