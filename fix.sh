#!/usr/bin/env bash
#
#  fix.sh ─ edit_file tool: patch-based edits + tool-dictated rendering
#
#  ▸ internal/tools/diff.go        NEW  patch model + unified diff builder
#  ▸ internal/tools/edit_file.go   NEW  edit_file tool (Review/Execute/RenderRequest)
#  ▸ internal/tools/registry.go    RequestRenderer interface + registry dispatch
#  ▸ internal/tools/shell.go       ShellTool renders its command as ```sh
#  ▸ sgpt/v1 proto                 ToolCallMetadata.diff (review-time diff)
#  ▸ cli/tui/timeline              generic RequestRenderer hook (no tool leakage)
#  ▸ session/chat screen/cmd       wiring
#
set -euo pipefail
cd "$(dirname "$0")"

BOLD=$'\033[1m'; GREEN=$'\033[32m'; CYAN=$'\033[36m'; YELLOW=$'\033[33m'; RED=$'\033[31m'; RESET=$'\033[0m'
step() { printf "\n%s▸ %s%s\n" "${BOLD}${CYAN}" "$1" "${RESET}"; }
ok()   { printf "  %s✓%s %s\n" "${GREEN}" "${RESET}" "$1"; }
warn() { printf "  %s⚠%s %s\n" "${YELLOW}" "${RESET}" "$1"; }
die()  { printf "  %s✗ %s%s\n" "${RED}" "$1" "${RESET}"; exit 1; }

# replace FILE OLD NEW — exact-match, must match exactly once.
# replace FILE OLD NEW — exact match first, else whitespace-insensitive.
replace() {
  OLD="$2" NEW="$3" python3 - "$1" <<'PY' || die "edit failed: $1"
import os, re, sys
path, old, new = sys.argv[1], os.environ["OLD"], os.environ["NEW"]
src = open(path).read()
if src.count(old) == 1:
    open(path, "w").write(src.replace(old, new))
    raise SystemExit(0)
# Fallback: match each line ignoring leading/trailing whitespace, so the
# edit survives tab/space mangling of the script itself.
pattern = r"[ \t]*\n".join(r"[ \t]*" + re.escape(l.strip()) for l in old.split("\n"))
matches = re.findall(pattern, src)
if len(matches) != 1:
    sys.exit(f"{path}: expected exactly 1 match, found {len(matches)}")
open(path, "w").write(src.replace(matches[0], new))
PY
  ok "$1"
}
printf "%s┌──────────────────────────────────────────────────┐%s\n" "${BOLD}" "${RESET}"
printf "%s│  sgpt · edit_file tool + diff review rendering   │%s\n" "${BOLD}" "${RESET}"
printf "%s└──────────────────────────────────────────────────┘%s\n" "${BOLD}" "${RESET}"

# ────────────────────────────────────────────────────────────────────
step "internal/tools/diff.go (new)"
cat > internal/tools/diff.go <<'EOF'
package tools

import (
  "fmt"
  "strings"
)

const diffContextLines = 3

type patch struct {
  Search  string `json:"search"`
  Replace string `json:"replace"`
}

// applyPatches applies exact search/replace patches sequentially and builds a
// unified-style diff. The same code path serves review (dry-run) and
// execution, so the diff the user approves is exactly what gets applied.
func applyPatches(path, content string, patches []patch) (string, string, error) {
  var diff strings.Builder
  diff.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", path, path))
  // Line drift from earlier patches, so later hunk headers stay accurate.
  lineDelta := 0

  for i, p := range patches {
    if p.Search == "" {
      return "", "", fmt.Errorf("patch %d: empty search text", i+1)
    }
    count := strings.Count(content, p.Search)
    if count == 0 {
      return "", "", fmt.Errorf("patch %d: search text not found in %s", i+1, path)
    }
    if count > 1 {
      return "", "", fmt.Errorf("patch %d: search text matches %d locations in %s; add more context", i+1, count, path)
    }

    index := strings.Index(content, p.Search)
    // Expand the match to full lines so the diff shows complete lines.
    lineStart := strings.LastIndex(content[:index], "\n") + 1
    lineEnd := index + len(p.Search)
    if newline := strings.Index(content[lineEnd:], "\n"); newline != -1 {
      lineEnd += newline
    } else {
      lineEnd = len(content)
    }

    removed := strings.Split(content[lineStart:lineEnd], "\n")
    added := strings.Split(strings.Replace(content[lineStart:lineEnd], p.Search, p.Replace, 1), "\n")

    allLines := strings.Split(content, "\n")
    startLine := strings.Count(content[:lineStart], "\n") // 0-based
    contextStart := max(0, startLine-diffContextLines)
    afterRemoved := startLine + len(removed)
    contextEnd := min(len(allLines), afterRemoved+diffContextLines)

    oldCount := (startLine - contextStart) + len(removed) + (contextEnd - afterRemoved)
    newCount := (startLine - contextStart) + len(added) + (contextEnd - afterRemoved)
    diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", contextStart+1, oldCount, contextStart+1+lineDelta, newCount))
    for _, line := range allLines[contextStart:startLine] {
      diff.WriteString(" " + line + "\n")
    }
    for _, line := range removed {
      diff.WriteString("-" + line + "\n")
    }
    for _, line := range added {
      diff.WriteString("+" + line + "\n")
    }
    for _, line := range allLines[afterRemoved:contextEnd] {
      diff.WriteString(" " + line + "\n")
    }

    lineDelta += len(added) - len(removed)
    content = strings.Replace(content, p.Search, p.Replace, 1)
  }
  return content, diff.String(), nil
}
EOF
ok "internal/tools/diff.go"

# ────────────────────────────────────────────────────────────────────
step "internal/tools/edit_file.go (new)"
cat > internal/tools/edit_file.go <<'EOF'
package tools

import (
  "context"
  "encoding/json"
  "fmt"
  "os"
  "strings"

  aipb "github.com/malonaz/core/genproto/ai/v1"
  jsonpb "github.com/malonaz/core/genproto/json/v1"

  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// EditFile is the tool definition for patch-based file editing.
var EditFile = &aipb.Tool{
  Name:        "edit_file",
  Description: "Edit a file by applying one or more patches. Each patch replaces an exact, unique occurrence of `search` with `replace`. Include enough surrounding context in `search` to make it unique within the file. Patches are applied sequentially.",
  JsonSchema: &jsonpb.Schema{
    Type: "object",
    Properties: map[string]*jsonpb.Schema{
      "path": {Type: "string", Description: "Path of the file to edit"},
      "patches": {
        Type:        "array",
        Description: "Patches applied sequentially, each an exact search/replace",
        Items: &jsonpb.Schema{
          Type: "object",
          Properties: map[string]*jsonpb.Schema{
            "search":  {Type: "string", Description: "Exact text to find (must match exactly once)"},
            "replace": {Type: "string", Description: "Replacement text"},
          },
          Required: []string{"search", "replace"},
        },
      },
    },
    Required: []string{"path", "patches"},
  },
  Annotations: map[string]string{
    ToolHandlerIDAnnotation: HandlerIDEditFile,
  },
}

type editFileArguments struct {
  Path    string  `json:"path"`
  Patches []patch `json:"patches"`
}

func parseEditFileArguments(toolCall *aipb.ToolCall) (*editFileArguments, error) {
  bytes, err := toolCallArgumentsJSON(toolCall)
  if err != nil {
    return nil, err
  }
  arguments := &editFileArguments{}
  if err := json.Unmarshal(bytes, arguments); err != nil {
    return nil, fmt.Errorf("parsing tool arguments: %w", err)
  }
  if arguments.Path == "" {
    return nil, fmt.Errorf("no path specified")
  }
  if len(arguments.Patches) == 0 {
    return nil, fmt.Errorf("no patches specified")
  }
  return arguments, nil
}

// EditFileTool applies search/replace patches to files on the user's system.
type EditFileTool struct{}

func (t *EditFileTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
  arguments, err := parseEditFileArguments(toolCall)
  if err != nil {
    return nil, err
  }
  // File mutation: never auto-execute.
  metadata := &sgptpb.ToolCallMetadata{
    DisplayMessage: &sgptpb.DisplayMessage{
      Content: fmt.Sprintf("Editing %s (%d patch(es))", arguments.Path, len(arguments.Patches)),
    },
  }
  contentBytes, err := os.ReadFile(arguments.Path)
  if err != nil {
    // Surface the failure in the review UI rather than erroring the turn;
    // Execute produces the error result the model can react to.
    metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
    return metadata, nil
  }
  // Dry-run: the user reviews the exact diff that will apply.
  if _, diff, err := applyPatches(arguments.Path, string(contentBytes), arguments.Patches); err != nil {
    metadata.DisplayMessage.Content = fmt.Sprintf("Edit will fail: %v", err)
  } else {
    metadata.Diff = diff
  }
  return metadata, nil
}

func (t *EditFileTool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
  arguments, err := parseEditFileArguments(toolCall)
  if err != nil {
    return nil, err
  }
  info, err := os.Stat(arguments.Path)
  if err != nil {
    return nil, fmt.Errorf("stat %s: %w", arguments.Path, err)
  }
  contentBytes, err := os.ReadFile(arguments.Path)
  if err != nil {
    return nil, fmt.Errorf("reading %s: %w", arguments.Path, err)
  }
  // Re-apply at execution time: the file may have changed since review.
  patched, _, err := applyPatches(arguments.Path, string(contentBytes), arguments.Patches)
  if err != nil {
    return nil, err
  }
  if err := os.WriteFile(arguments.Path, []byte(patched), info.Mode()); err != nil {
    return nil, fmt.Errorf("writing %s: %w", arguments.Path, err)
  }
  return &aipb.ToolResult{
    ToolName:   toolCall.Name,
    ToolCallId: toolCall.Id,
    Result: &aipb.ToolResult_Content{
      Content: fmt.Sprintf("Applied %d patch(es) to %s", len(arguments.Patches), arguments.Path),
    },
  }, nil
}

// RenderRequest renders the review-time diff instead of raw JSON arguments.
// The diff is persisted on the call's metadata so it survives chat reloads
// even though the underlying file has since changed.
func (t *EditFileTool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
  metadata, err := ParseToolCallMetadata(toolCall)
  if err != nil || metadata.GetDiff() == "" {
    return "", false
  }
  return fmt.Sprintf("```diff\n%s\n```", strings.TrimSuffix(metadata.GetDiff(), "\n")), true
}

var (
  _ Tool            = (*EditFileTool)(nil)
  _ RequestRenderer = (*EditFileTool)(nil)
)
EOF
ok "internal/tools/edit_file.go"

# ────────────────────────────────────────────────────────────────────
step "internal/tools/registry.go — handler ID + RequestRenderer"
replace internal/tools/registry.go \
'  HandlerIDEngine    = "engine"' \
'  HandlerIDEngine    = "engine"
  HandlerIDEditFile  = "edit_file"'

cat >> internal/tools/registry.go <<'EOF'

// RequestRenderer is implemented by tools that dictate how their request
// renders in the timeline (e.g. edit_file renders a diff).
type RequestRenderer interface {
  Tool
  // RenderRequest returns markdown for the request; returning false falls
  // back to the default raw-JSON rendering.
  RenderRequest(toolCall *aipb.ToolCall) (string, bool)
}

// RenderRequest returns tool-provided request markdown for a call, if its
// tool implements RequestRenderer.
func (r *Registry) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
  tool, err := r.lookup(toolCall)
  if err != nil {
    return "", false
  }
  renderer, ok := tool.(RequestRenderer)
  if !ok {
    return "", false
  }
  return renderer.RenderRequest(toolCall)
}
EOF
ok "RequestRenderer dispatch appended"

# ────────────────────────────────────────────────────────────────────
step "internal/tools/shell.go — render command as sh fence"
cat >> internal/tools/shell.go <<'EOF'

// RenderRequest renders the command as a shell fence instead of raw JSON.
func (t *ShellTool) RenderRequest(toolCall *aipb.ToolCall) (string, bool) {
  arguments, err := parseShellCommandArguments(toolCall)
  if err != nil {
    return "", false
  }
  display := arguments.Command
  if arguments.WorkingDirectory != "" {
    display = fmt.Sprintf("cd %s && %s", arguments.WorkingDirectory, arguments.Command)
  }
  return fmt.Sprintf("```sh\n%s\n```", display), true
}

var _ RequestRenderer = (*ShellTool)(nil)
EOF
ok "ShellTool.RenderRequest appended"

# ────────────────────────────────────────────────────────────────────
step "internal/tools/BUILD.plz — new sources"
replace internal/tools/BUILD.plz \
'        "read_files.go",' \
'        "diff.go",
        "edit_file.go",
        "read_files.go",'

# ────────────────────────────────────────────────────────────────────
step "proto — ToolCallMetadata.diff"
PROTO_FILE=$(grep -rl "message ToolCallMetadata" sgpt/ 2>/dev/null | head -1 || true)
if [[ -n "${PROTO_FILE}" ]]; then
  PROTO="${PROTO_FILE}" python3 - <<'PY' || die "proto edit failed"
import os, re
path = os.environ["PROTO"]
src = open(path).read()
if "string diff" in src:
    raise SystemExit(0)  # already applied
pattern = re.compile(r"(message ToolCallMetadata \{.*?)(\n\})", re.DOTALL)
insert = "\n  // Unified diff computed at review time by the edit_file tool.\n  string diff = 3;"
src, count = pattern.subn(lambda m: m.group(1) + insert + m.group(2), src, count=1)
if count != 1:
    raise SystemExit("could not locate message ToolCallMetadata body")
open(path, "w").write(src)
PY
  ok "${PROTO_FILE}"
  warn "verify tag 3 is free in ToolCallMetadata, then regenerate protos"
else
  warn "ToolCallMetadata proto not found — add manually: string diff = 3;"
fi

# ────────────────────────────────────────────────────────────────────
step "cli/tui/timeline/items.go — generic RequestRenderer hook"
replace cli/tui/timeline/items.go \
'// ---- ToolCallItem: request/response pair rendered adjacently ----' \
'// RequestRenderer lets a tool dictate how its request renders in the
// timeline; unset (or declining) falls back to raw JSON arguments.
type RequestRenderer interface {
  RenderRequest(toolCall *aipb.ToolCall) (string, bool)
}

// ---- ToolCallItem: request/response pair rendered adjacently ----'

replace cli/tui/timeline/items.go \
'  ToolCall  *aipb.ToolCall
  Result    *aipb.ToolResult
  Executing bool
}' \
'  ToolCall  *aipb.ToolCall
  Result    *aipb.ToolResult
  Executing bool
  // RequestRenderer, when set, overrides the raw-JSON request rendering.
  RequestRenderer RequestRenderer
}'

replace cli/tui/timeline/items.go \
'func (i *ToolCallItem) request(ctx RenderContext) string {
  // Full payload — inspection during review was the whole point.' \
'func (i *ToolCallItem) request(ctx RenderContext) string {
  // Tools may dictate their own presentation (e.g. edit_file'"'"'s diff).
  if i.RequestRenderer != nil {
    if md, ok := i.RequestRenderer.RenderRequest(i.ToolCall); ok {
      return renderMarkdown(ctx, i.seq, true, markdown.ParseBlocks(md)...)
    }
  }
  // Full payload — inspection during review was the whole point.'

replace cli/tui/timeline/items.go \
'func BuildChatItems(messages []*sgptpb.Message, streamingMessage *aipb.Message, executingToolCallID string) []Item {' \
'func BuildChatItems(messages []*sgptpb.Message, streamingMessage *aipb.Message, executingToolCallID string, requestRenderer RequestRenderer) []Item {'

replace cli/tui/timeline/items.go \
'    items = appendMessageItems(items, chatMessage.GetMessage(), messageIndex, true, toolCallIDToResult, executingToolCallID)' \
'    items = appendMessageItems(items, chatMessage.GetMessage(), messageIndex, true, toolCallIDToResult, executingToolCallID, requestRenderer)'

replace cli/tui/timeline/items.go \
'    items = appendMessageItems(items, streamingMessage, len(messages), false, toolCallIDToResult, executingToolCallID)' \
'    items = appendMessageItems(items, streamingMessage, len(messages), false, toolCallIDToResult, executingToolCallID, requestRenderer)'

replace cli/tui/timeline/items.go \
'  toolCallIDToResult map[string]*aipb.ToolResult,
  executingToolCallID string,
) []Item {' \
'  toolCallIDToResult map[string]*aipb.ToolResult,
  executingToolCallID string,
  requestRenderer RequestRenderer,
) []Item {'

replace cli/tui/timeline/items.go \
'          Executing: executingToolCallID != "" && toolCall.GetId() == executingToolCallID,
        })' \
'          Executing:       executingToolCallID != "" && toolCall.GetId() == executingToolCallID,
          RequestRenderer: requestRenderer,
        })'

# ────────────────────────────────────────────────────────────────────
step "internal/session/session.go — expose registry"
cat >> internal/session/session.go <<'EOF'

// Registry exposes the tool registry so the TUI can delegate tool-dictated
// request rendering (timeline.RequestRenderer).
func (s *Session) Registry() *tools.Registry {
  return s.registry
}
EOF
ok "Session.Registry() appended"

# ────────────────────────────────────────────────────────────────────
step "callers — thread the renderer through"
replace cli/tui/screen/chat.go \
'  items = append(items, timeline.BuildChatItems(
    m.session.Chat().GetMetadata().GetMessages(),
    m.session.StreamingMessage(),
    m.session.ExecutingToolCallID(),
  )...)' \
'  items = append(items, timeline.BuildChatItems(
    m.session.Chat().GetMetadata().GetMessages(),
    m.session.StreamingMessage(),
    m.session.ExecutingToolCallID(),
    m.session.Registry(),
  )...)'

replace cli/tui/screen/menu/view.go \
'  items := timeline.BuildChatItems(chat.GetMetadata().GetMessages(), nil, "")' \
'  items := timeline.BuildChatItems(chat.GetMetadata().GetMessages(), nil, "", nil)'
warn "menu preview passes nil renderer — falls back to JSON args (no registry there)"

replace cli/chat/cmd.go \
'      registry.Register(tools.HandlerIDReadFiles, &tools.ReadFilesTool{})' \
'      registry.Register(tools.HandlerIDReadFiles, &tools.ReadFilesTool{})
      registry.Register(tools.HandlerIDEditFile, &tools.EditFileTool{})'

# ────────────────────────────────────────────────────────────────────
step "gofmt"
gofmt -w \
  internal/tools/diff.go internal/tools/edit_file.go internal/tools/registry.go \
  internal/tools/shell.go internal/session/session.go \
  cli/tui/timeline/items.go cli/tui/screen/chat.go cli/tui/screen/menu/view.go \
  cli/chat/cmd.go
ok "formatted"

# ────────────────────────────────────────────────────────────────────
printf "\n%s┌──────────────────────────────────────────────────┐%s\n" "${GREEN}${BOLD}" "${RESET}"
printf "%s│                  ✓ all done                      │%s\n" "${GREEN}${BOLD}" "${RESET}"
printf "%s└──────────────────────────────────────────────────┘%s\n" "${GREEN}${BOLD}" "${RESET}"
printf "\nNext steps:\n"
printf "  1. Regenerate protos (ToolCallMetadata.diff), e.g. %splz build //sgpt/...%s\n" "${BOLD}" "${RESET}"
printf "  2. Advertise %stools.EditFile%s wherever ShellCommand/ReadFiles are passed to registry.AddTools\n" "${BOLD}" "${RESET}"
printf "  3. %splz build //...%s\n" "${BOLD}" "${RESET}"
