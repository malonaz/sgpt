@instructions(
Explain the .sgpt artifact system end to end: graphs (.sgpt.json), knowledge nodes (*.node), roles (*.role) and tool sets (*.toolset). Cover field ownership, selector addressing (//dir:title, root shorthands, /..., @import// prefixes), how sgpt chat consumes each artifact kind (-g and role graph_nodes for nodes, --role for roles, --tool for builtins and tool sets, the read_nodes tool), the standardized node rendering, and the implementation map. Precise enough that an agent can implement against it.
)

@summary(
The .sgpt artifact system: knowledge nodes (*.node), roles (*.role) and tool sets (*.toolset) discovered in .sgpt/ directories, addressed please-style (//dir:title, @import//dir:title), injected via -g/--role/--tool and explored via the read_nodes tool.
)

@file("sgpt/v1/graph.proto")

## The .sgpt artifact system

A repo becomes a **graph** by holding a `.sgpt.json` file at its root — the repo-local sgpt configuration (`sgpt.v1.Configuration`, merged as an override into the user's global `~/.config/sgpt/.sgpt.json`). Its `title` describes the repo and its top-level `ignore` patterns (gitignore syntax) apply on top of the repo's `.gitignore` files whenever the tree is walked. There is no separate graph descriptor.

Any directory in the tree can then hold a `.sgpt/` directory containing artifacts, one file per artifact, where the file stem is the artifact's **title**:

| File | Payload | Purpose |
|------|---------|---------|
| `{title}.node.md` | `sgpt.v1.Node` | Curated knowledge about the directory subtree |
| `{title}.role.md` | `sgpt.v1.Role` | A persona: system prompt + default files/tools/nodes |
| `{title}.toolset` | `sgpt.v1.ToolSet` (JSON) | A remote gRPC tool engine to advertise to the model |

Discovery is one ignore-aware BFS walk of the tree. Identity is deterministic: every artifact's `name` is stamped from its location — never read from the file.

### Markdown artifact format (.node.md, .role.md)

Lines starting with an `at`-directive — `at-sign` + `name` + `(...)` at column 0 — carry the structured fields; **everything else is the body** (a node's content, a role's prompt). Two directive shapes:
- argument form, inline, all arguments double-quoted: `label("key", "value")` style;
- text form, inline or multiline, a multiline directive is terminated by a lone `)` line.

Invalid or unknown directives are hard errors — never silently folded into the body. Prose lines mentioning `@something` without a directive-shaped `name(` opener are body, not directives.

Node directives: `instructions` (text: what the node should capture — human prompt), `summary` (text), `label("key", "value")`, `file("root/relative/path")`.

Role directives: `alias("go")`, `model("providers/x/models/y")`, `tool("exec_shell")`, `role("//dir:other")` (include), `node("//dir:title")` (inject a graph node), `file("root/relative/path")`.

`.toolset` files remain strict JSON: `{"engine_service": "...", "tool_sets": [...]}`.

## Addressing (selectors)

Please-style, relative to the graph root:

- `//dir:title` — one artifact (`//user/service:architecture`).
- `//title` — an artifact at the root (directory-first: a directory of the same name shadows it); bare `title` also accepted as input.
- `//dir` — every node of that directory (`//` for the root's).
- `//dir/...` — nodes of the directory plus its whole subtree, BFS top-down (never offered by completion; type it).
- `@{import}//{selector}` — an artifact of an imported repo, e.g. `@github.com/malonaz/core//go/grpc:architecture`.

Imports are declared in the sgpt configuration (top-level `imports`: `{name, path}`). An imported repo must have its own `.sgpt.json` root marker and is scanned under **its own** rules — its `.gitignore` files and its configuration's `ignore` — never the importer's.

## Nodes

Field ownership on `sgpt.v1.Node`:
- **Human-owned**: `instructions` (what this node should capture), `labels`, `files` (root-relative paths pulled in verbatim wherever the node renders — reference well-documented files like proto APIs instead of duplicating them).
- **Knowledge payload**: `summary` (directive) and `content` (the markdown body).
- **Deterministic**: `name`, derived from the file location.

How chats consume nodes:
1. **Injection**: `sgpt chat -g <selector>` (repeatable, combinable with regular `-f` file injection) injects each selected node as a context message. Roles contribute selectors via `graph_nodes`.
2. **Standardized rendering**: `# name`, summary, content, then `Parent nodes:` / `Child nodes:` lists (name — summary), then `## File: path` blocks for pulled-in files. Parents are all ancestor directories' nodes; children are the nearest node-bearing descendant layer. These lists are the graph's edges.
3. **Exploration**: the `read_nodes` tool (auto-enabled with any graph selector, read-only, auto-executing) takes node names and returns `{content, parents, children, files}` per node, names fully qualified — the model can follow any reference it sees, across repos.

Intended flow: inject a small map, let the model walk edges on demand.

## Roles

A `.role.md` file is a persona: the body is the system prompt, plus defaults merged into the chat via directives: `file` (root-relative to the role's repo), `tool`, `node` (graph selectors), `model`, and `role` — other roles to include, expanded depth-first (their prompts prepended, their files/tools/nodes merged).

Selected with `--role //dir:title`, by `alias`, or `@import//dir:title` for an imported repo's role. An imported role stays coherent with its home repo: its files resolve against that repo's root, and every selector it carries is auto-qualified with the import prefix. The configuration's `chat.default_role` names the default selector.

## Tools and tool sets

Two kinds of tools can be advertised to the model:
- **Builtins**: `read_files`, `exec_shell`, `diff`, `replace`, `agent`, `read_nodes` — always available by name.
- **Tool sets** (`.toolset` JSON files): a remote gRPC tool engine — `engine_service` names a gRPC client from the configuration, `tool_sets` holds the engine's creation requests. Addressed by selector (`--tool //gateway:engine`), dialed lazily on first enablement, toggleable mid-chat via the tool picker.

A role's `tools` list mixes both freely.

## Implementation map

- `internal/graph` — discovery core: `graph.go` (generic `File[T]`, `FindRoot` anchoring on `.sgpt.json`), `markdown.go` (the strict directive parser + per-kind builders), `tree.go` (`Scan`: ignore-aware BFS), `select.go` (selector parsing/resolution, ambiguity checks, completion listings), `related.go` (node edges + standardized `Render`), `forest.go` (`Forest`: primary tree + lazy imports, `@import//` routing, role qualification).
- `internal/tool/nodes` — the `read_nodes` builtin (proto: `ToolService.ReadNodes`).
- `internal/tool/rpc` — tool-engine manager fed by discovered `.toolset` files.
- `internal/role` — role registry/expansion and system-prompt templating.
- `internal/ignore` — gitignore-syntax matching used by every walk.
- `cli/chat/cmd.go` — wires it all: forest construction, `-g`/`--role`/`--tool` flags and completion, virtual injection via `session.Params.InjectedFileContents`.
