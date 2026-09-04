# SGPT

A terminal-native AI coding agent. Multi-tab chat TUI, reviewable tool
calls, repo-scoped roles and a committed knowledge base — driven from
`.sgpt/` artifacts that travel with your code.

Docs: **https://docs.sgpt.malonaz.com**

## Install

```bash
go install github.com/malonaz/sgpt/cmd/sgpt-cli@latest
mv "$(go env GOPATH)/bin/sgpt-cli" ~/.local/bin/sgpt
```

Or from a checkout (please): `plz build //cmd/sgpt-cli && ./scripts/install.sh`.

## Configure

`~/.config/sgpt/.sgpt.json` is [Jsonnet](https://jsonnet.org) evaluated with
`env()` available, so secrets stay in the environment:

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
    default_tools: ["search_lores"],
  },
  models: [{ name: "providers/anthropic/models/claude-opus-5", alias: "opus" }],
  imports: [{ name: "core", path: "~/core" }],
}
```

Any `.sgpt.json` found walking up from the cwd is merged on top — each repo
carries its own title, imports, ignores and default role.

## Use

```bash
sgpt chat                          # new chat, default role
sgpt chat -r reviewer -m opus      # role + model alias
sgpt chat -f internal/... -f go.mod # inject files (dir/... recurses)
sgpt chat -c                       # continue the last chat
sgpt chat --tool jira              # enable a remote tool engine
```

Inside the TUI: `ctrl+j` sends, `alt+y` accepts a tool call, `ctrl+t` opens
a tab, `alt+m` the chat menu, `alt+h` the full keymap.

## The `.sgpt/` graph

Any directory may hold artifacts, addressed please-style (`//dir:title`,
`@import//dir:title`):

| Artifact | File | Purpose |
|---|---|---|
| Role | `.sgpt/{title}.role.md` | Persona: `@alias`, `@model`, `@file`, `@tool`, `@role` directives + a markdown prompt |
| Tool set | `.sgpt/{title}.toolset` | gRPC service methods exposed to the model via a remote tool engine |
| Lore | `.sgpt/lores/{id}.md` | Durable knowledge the agent searches with `search_lores` |

Built-in tools: `read_files`, `exec_shell`, `diff`, `replace`,
`search_lores`, `agent` (sub-agents in their own tab). Side-effect-free
tools auto-execute; everything else waits for your review.

## Develop

```bash
plz build //...   # build
plz test //...    # test
plz lint          # codegen → genproto/, wollemi, gofmt, buf
plz tidy          # go mod tidy + refetch third_party/go, then lint
```

Generated code under `genproto/` is committed so `go build` works without
please; never edit it by hand.
