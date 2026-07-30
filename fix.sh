#!/usr/bin/env bash
#
# Refactors sgpt:
#   1. internal/store  — single RPC boundary (chats, models, streaming). TUI/session never touch raw clients.
#   2. internal/tools  — unified Tool interface (Review/Execute) + Registry; kills Handler/HandleResult duplication.
#   3. internal/session — pure turn state machine, moved out of cli/cli_service, persists via store.
#   4. TUI de-RPC      — app/menu consume the store; duplicated chat CRUD, uuid schemes & favorite logic removed.
#   5. Cleanup         — cache double-path bug, dead code (allTools, truncateLines, containsIgnoreCase, fake imports).
#
set -euo pipefail
cd "$(dirname "$0")"

############################################
# Deletions
############################################
rm -rf cli/cli_service
rm -f cli/chat/cache.go
rm -f internal/tools/handler.go

mkdir -p internal/store internal/session

############################################
# internal/cache — fix double "sgpt" path segment
############################################
cat > internal/cache/cache.go <<'EOF'
package cache

import (
  "os"
  "path/filepath"
  "time"

  "github.com/malonaz/core/go/pbutil"
  "google.golang.org/protobuf/proto"
)

func Dir() string {
  cacheDir, err := os.UserCacheDir()
  if err != nil {
    cacheDir = os.TempDir()
  }
  return filepath.Join(cacheDir, "sgpt")
}

func path(key string) string {
  return filepath.Join(Dir(), key)
}

// Get loads a cached proto message. Returns nil, false if missing or expired.
func Get[T proto.Message](key string, maxAge time.Duration, empty T) (T, bool) {
  cachePath := path(key)
  info, err := os.Stat(cachePath)
  if err != nil || time.Since(info.ModTime()) > maxAge {
    return empty, false
  }
  data, err := os.ReadFile(cachePath)
  if err != nil {
    return empty, false
  }
  if err := pbutil.Unmarshal(data, empty); err != nil {
    return empty, false
  }
  return empty, true
}

// Store writes a proto message to the cache under the given key.
func Store[T proto.Message](key string, message T) error {
  data, err := pbutil.Marshal(message)
  if err != nil {
    return err
  }
  cachePath := path(key)
  if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
    return err
  }
  return os.WriteFile(cachePath, data, 0644)
}
EOF

############################################
# internal/store — single RPC boundary
############################################
cat > internal/store/store.go <<'EOF'
// Package store is the single RPC boundary for chat and model operations.
// All request construction, ID generation, field masks and caching live here
// so the TUI and session layers never touch raw gRPC clients.
package store

import (
  "context"
  "fmt"

  aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
  "github.com/malonaz/core/go/pbutil/pbfieldmask"
  "github.com/malonaz/core/go/uuid"
  "google.golang.org/protobuf/proto"

  sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

const (
  // FavoriteTag marks a chat as a favorite.
  FavoriteTag = "favorite"
  // FavoriteFilter is the server-side filter matching favorite chats.
  FavoriteFilter = `tags:"favorite"`
)

// Store wraps the sgpt and ai service clients.
type Store struct {
  configuration     *sgptpb.Configuration
  aiServiceClient   aiservicepb.AiServiceClient
  chatServiceClient sgptservicepb.SgptServiceClient
}

// New instantiates a store.
func New(
  configuration *sgptpb.Configuration,
  aiServiceClient aiservicepb.AiServiceClient,
  chatServiceClient sgptservicepb.SgptServiceClient,
) *Store {
  return &Store{
    configuration:     configuration,
    aiServiceClient:   aiServiceClient,
    chatServiceClient: chatServiceClient,
  }
}

// newChatID returns the last 8 characters of a v7 UUID. The first characters
// of a v7 UUID are a timestamp prefix that collides for chats created within
// the same ~65s window; the last ones are random.
func newChatID() string {
  chatID := uuid.MustNewV7().String()
  return chatID[len(chatID)-8:]
}

// CreateChat persists a new chat.
func (s *Store) CreateChat(ctx context.Context, chat *sgptpb.Chat) (*sgptpb.Chat, error) {
  createChatRequest := &sgptservicepb.CreateChatRequest{
    RequestId: uuid.MustNewV7().String(),
    ChatId:    newChatID(),
    Chat:      chat,
  }
  createdChat, err := s.chatServiceClient.CreateChat(ctx, createChatRequest)
  if err != nil {
    return nil, fmt.Errorf("creating chat: %w", err)
  }
  return createdChat, nil
}

// UpdateChat persists the given paths of a chat.
func (s *Store) UpdateChat(ctx context.Context, chat *sgptpb.Chat, paths ...string) (*sgptpb.Chat, error) {
  updateChatRequest := &sgptservicepb.UpdateChatRequest{
    Chat:       chat,
    UpdateMask: pbfieldmask.FromPaths(paths...).MustValidate(&sgptpb.Chat{}).Proto(),
  }
  updatedChat, err := s.chatServiceClient.UpdateChat(ctx, updateChatRequest)
  if err != nil {
    return nil, fmt.Errorf("updating chat: %w", err)
  }
  return updatedChat, nil
}

// GetChat fetches a chat by resource name.
func (s *Store) GetChat(ctx context.Context, name string) (*sgptpb.Chat, error) {
  getChatRequest := &sgptservicepb.GetChatRequest{Name: name}
  chat, err := s.chatServiceClient.GetChat(ctx, getChatRequest)
  if err != nil {
    return nil, fmt.Errorf("getting chat: %w", err)
  }
  return chat, nil
}

// DeleteChat deletes a chat by resource name.
func (s *Store) DeleteChat(ctx context.Context, name string) error {
  deleteChatRequest := &sgptservicepb.DeleteChatRequest{Name: name}
  if _, err := s.chatServiceClient.DeleteChat(ctx, deleteChatRequest); err != nil {
    return fmt.Errorf("deleting chat: %w", err)
  }
  return nil
}

// ForkChat clones a chat into a new resource.
func (s *Store) ForkChat(ctx context.Context, chat *sgptpb.Chat) (*sgptpb.Chat, error) {
  forkedChat := proto.Clone(chat).(*sgptpb.Chat)
  forkedChat.Name = ""
  return s.CreateChat(ctx, forkedChat)
}

// ListChats returns a page of chats, most recent first.
func (s *Store) ListChats(ctx context.Context, pageSize int32, pageToken, filter string) ([]*sgptpb.Chat, string, error) {
  listChatsRequest := &sgptservicepb.ListChatsRequest{
    PageSize:  pageSize,
    PageToken: pageToken,
    Filter:    filter,
    OrderBy:   "create_time desc",
  }
  listChatsResponse, err := s.chatServiceClient.ListChats(ctx, listChatsRequest)
  if err != nil {
    return nil, "", fmt.Errorf("listing chats: %w", err)
  }
  return listChatsResponse.Chats, listChatsResponse.NextPageToken, nil
}

// ListFavoriteChats returns the first page of favorite chats.
func (s *Store) ListFavoriteChats(ctx context.Context, pageSize int32) ([]*sgptpb.Chat, error) {
  chats, _, err := s.ListChats(ctx, pageSize, "", FavoriteFilter)
  return chats, err
}

// LatestChat returns the most recently created chat.
func (s *Store) LatestChat(ctx context.Context) (*sgptpb.Chat, error) {
  chats, _, err := s.ListChats(ctx, 1, "", "")
  if err != nil {
    return nil, err
  }
  if len(chats) == 0 {
    return nil, fmt.Errorf("no chat found")
  }
  return chats[0], nil
}

// SearchChats performs a full-text search over chat transcripts.
func (s *Store) SearchChats(ctx context.Context, query string, pageSize int32, pageToken string) ([]*sgptpb.Chat, string, error) {
  searchChatsRequest := &sgptservicepb.SearchChatsRequest{
    Query:     query,
    PageSize:  pageSize,
    PageToken: pageToken,
  }
  searchChatsResponse, err := s.chatServiceClient.SearchChats(ctx, searchChatsRequest)
  if err != nil {
    return nil, "", fmt.Errorf("searching chats: %w", err)
  }
  return searchChatsResponse.Chats, searchChatsResponse.NextPageToken, nil
}

// SetFavorite sets or clears the favorite tag on a chat and persists it.
func (s *Store) SetFavorite(ctx context.Context, chat *sgptpb.Chat, favorite bool) (*sgptpb.Chat, error) {
  SetTag(chat, FavoriteTag, favorite)
  return s.UpdateChat(ctx, chat, "tags")
}

// HasTag reports whether a chat carries the given tag.
func HasTag(chat *sgptpb.Chat, tag string) bool {
  for _, existingTag := range chat.GetTags() {
    if existingTag == tag {
      return true
    }
  }
  return false
}

// SetTag adds or removes a tag on a chat in place.
func SetTag(chat *sgptpb.Chat, tag string, present bool) {
  tags := make([]string, 0, len(chat.GetTags())+1)
  for _, existingTag := range chat.GetTags() {
    if existingTag != tag {
      tags = append(tags, existingTag)
    }
  }
  if present {
    tags = append(tags, tag)
  }
  chat.Tags = tags
}
EOF

cat > internal/store/models.go <<'EOF'
package store

import (
  "context"
  "fmt"
  "time"

  aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
  aipb "github.com/malonaz/core/genproto/ai/v1"
  "github.com/malonaz/core/go/aip"
  "github.com/malonaz/core/go/grpc/middleware"

  "github.com/malonaz/sgpt/internal/cache"
  "github.com/malonaz/sgpt/internal/configuration"
)

const (
  modelsCacheKey    = "models_cache.pb"
  modelsCacheMaxAge = 24 * time.Hour
)

// ListModels returns models, served from the disk cache unless stale or forceRefresh is set.
func (s *Store) ListModels(ctx context.Context, forceRefresh bool) ([]*aipb.Model, error) {
  if !forceRefresh {
    listModelsResponse, ok := cache.Get(modelsCacheKey, modelsCacheMaxAge, &aiservicepb.ListModelsResponse{})
    if ok && len(listModelsResponse.Models) > 0 {
      return listModelsResponse.Models, nil
    }
  }
  ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
  defer cancel()
  // Only name & ttt are needed; a strict read mask keeps the payload small.
  ctx = middleware.WithReadMaskStrict(ctx, "name,ttt")
  listModelsRequest := &aiservicepb.ListModelsRequest{Parent: "providers/-"}
  models, err := aip.Paginate[*aipb.Model](ctx, listModelsRequest, s.aiServiceClient.ListModels)
  if err != nil {
    return nil, err
  }
  cache.Store(modelsCacheKey, &aiservicepb.ListModelsResponse{Models: models})
  return models, nil
}

// ResolveModel resolves a model name or configured alias to a full model,
// refreshing the cache once if the model is not found.
func (s *Store) ResolveModel(ctx context.Context, nameOrAlias string) (*aipb.Model, error) {
  modelName, err := configuration.ResolveModelAlias(s.configuration, nameOrAlias)
  if err != nil {
    return nil, err
  }
  for _, forceRefresh := range []bool{false, true} {
    models, err := s.ListModels(ctx, forceRefresh)
    if err != nil {
      return nil, err
    }
    for _, model := range models {
      if model.Name == modelName {
        return model, nil
      }
    }
  }
  return nil, fmt.Errorf("model not found: %s", modelName)
}

// TextToTextStream opens a streaming completion against the AI service.
func (s *Store) TextToTextStream(
  ctx context.Context,
  textToTextStreamRequest *aiservicepb.TextToTextStreamRequest,
) (aiservicepb.AiService_TextToTextStreamClient, error) {
  return s.aiServiceClient.TextToTextStream(ctx, textToTextStreamRequest)
}
EOF

cat > internal/store/BUILD.plz <<'EOF'
go_library(
    name = "store",
    srcs = [
        "models.go",
        "store.go",
    ],
    visibility = ["//..."],
    deps = [
        "//internal/cache",
        "//internal/configuration",
        "//sgpt/sgpt_service/v1",
        "//sgpt/v1",
        "//third_party/go:github.com__malonaz__core__go__aip",
        "//third_party/go:github.com__malonaz__core__go__grpc__middleware",
        "//third_party/go:github.com__malonaz__core__go__pbutil__pbfieldmask",
        "//third_party/go:github.com__malonaz__core__go__uuid",
        "//third_party/go:google.golang.org__protobuf__proto",
        "//third_party/proto:malonaz__core__genproto__ai__ai_service__v1",
        "//third_party/proto:malonaz__core__genproto__ai__v1",
    ],
)
EOF

############################################
# internal/tools — unified Tool interface + Registry
############################################
cat > internal/tools/registry.go <<'EOF'
package tools

import (
  "context"
  "encoding/json"
  "fmt"

  aipb "github.com/malonaz/core/genproto/ai/v1"

  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// ToolHandlerIDAnnotation routes a tool call to its registered Tool.
const ToolHandlerIDAnnotation = "sgpt.com/tool-handler-id"

const (
  HandlerIDShell     = "shell"
  HandlerIDReadFiles = "read_files"
  HandlerIDEngine    = "engine"
)

// Tool reviews and executes tool calls.
type Tool interface {
  // Review inspects a tool call as it arrives and returns display and
  // auto-execute metadata. It must not have side effects.
  Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error)
  // Execute runs the tool call and returns its result.
  Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error)
}

// Registry dispatches tool calls to registered tools and owns the tool
// definitions advertised to the model.
type Registry struct {
  handlerIDToTool map[string]Tool
  tools           []*aipb.Tool
  toolSets        []*aipb.ToolSet
}

// NewRegistry instantiates an empty registry.
func NewRegistry() *Registry {
  return &Registry{handlerIDToTool: map[string]Tool{}}
}

// Register binds a handler ID to a tool implementation.
func (r *Registry) Register(handlerID string, tool Tool) {
  r.handlerIDToTool[handlerID] = tool
}

// AddTools advertises tool definitions to the model.
func (r *Registry) AddTools(tools ...*aipb.Tool) {
  r.tools = append(r.tools, tools...)
}

// AddToolSets advertises tool sets to the model.
func (r *Registry) AddToolSets(toolSets ...*aipb.ToolSet) {
  r.toolSets = append(r.toolSets, toolSets...)
}

// Tools returns the advertised tool definitions.
func (r *Registry) Tools() []*aipb.Tool {
  return r.tools
}

// ToolSets returns the advertised tool sets.
func (r *Registry) ToolSets() []*aipb.ToolSet {
  return r.toolSets
}

// Handles reports whether a tool is registered for the given call.
func (r *Registry) Handles(toolCall *aipb.ToolCall) bool {
  _, ok := r.handlerIDToTool[toolCall.GetAnnotations()[ToolHandlerIDAnnotation]]
  return ok
}

func (r *Registry) lookup(toolCall *aipb.ToolCall) (Tool, error) {
  handlerID := toolCall.GetAnnotations()[ToolHandlerIDAnnotation]
  tool, ok := r.handlerIDToTool[handlerID]
  if !ok {
    return nil, fmt.Errorf("no tool registered for %q (handler_id=%q)", toolCall.GetName(), handlerID)
  }
  return tool, nil
}

// Review dispatches to the tool's Review and persists the resulting metadata
// onto the call's annotations — the single place this happens.
func (r *Registry) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
  tool, err := r.lookup(toolCall)
  if err != nil {
    return nil, err
  }
  metadata, err := tool.Review(ctx, toolCall)
  if err != nil {
    return nil, fmt.Errorf("reviewing tool call %q: %w", toolCall.GetName(), err)
  }
  if err := SetToolCallMetadata(toolCall, metadata); err != nil {
    return nil, err
  }
  return metadata, nil
}

// Execute dispatches to the tool's Execute.
func (r *Registry) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
  tool, err := r.lookup(toolCall)
  if err != nil {
    return nil, err
  }
  toolResult, err := tool.Execute(ctx, toolCall)
  if err != nil {
    return nil, fmt.Errorf("executing tool call %q: %w", toolCall.GetName(), err)
  }
  return toolResult, nil
}

// toolCallArgumentsJSON marshals a tool call's arguments to JSON bytes.
func toolCallArgumentsJSON(toolCall *aipb.ToolCall) ([]byte, error) {
  bytes, err := json.Marshal(toolCall.GetArguments().AsMap())
  if err != nil {
    return nil, fmt.Errorf("marshaling tool call arguments: %w", err)
  }
  return bytes, nil
}
EOF

cat > internal/tools/shell.go <<'EOF'
package tools

import (
  "context"
  "encoding/json"
  "fmt"
  "os/exec"

  aipb "github.com/malonaz/core/genproto/ai/v1"
  jsonpb "github.com/malonaz/core/genproto/json/v1"

  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// ShellCommand is the tool definition for shell execution.
var ShellCommand = &aipb.Tool{
  Name:        "exec_shell",
  Description: "Execute a shell command on the user's system. Use this when the user asks you to run commands, create files, or perform system operations.",
  JsonSchema: &jsonpb.Schema{
    Type: "object",
    Properties: map[string]*jsonpb.Schema{
      "command": {
        Type:        "string",
        Description: "The shell command to execute",
      },
      "working_directory": {
        Type:        "string",
        Description: "Optional working directory for the command execution. If not specified, uses current directory.",
      },
    },
    Required: []string{"command"},
  },
  Annotations: map[string]string{
    ToolHandlerIDAnnotation: HandlerIDShell,
  },
}

type shellCommandArguments struct {
  Command          string `json:"command"`
  WorkingDirectory string `json:"working_directory"`
}

func parseShellCommandArguments(toolCall *aipb.ToolCall) (*shellCommandArguments, error) {
  bytes, err := toolCallArgumentsJSON(toolCall)
  if err != nil {
    return nil, err
  }
  arguments := &shellCommandArguments{}
  if err := json.Unmarshal(bytes, arguments); err != nil {
    return nil, fmt.Errorf("parsing tool arguments: %w", err)
  }
  if arguments.Command == "" {
    return nil, fmt.Errorf("no command specified")
  }
  return arguments, nil
}

// ShellTool executes shell commands on the user's system.
type ShellTool struct{}

func (t *ShellTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
  arguments, err := parseShellCommandArguments(toolCall)
  if err != nil {
    return nil, err
  }
  display := arguments.Command
  if arguments.WorkingDirectory != "" {
    display = fmt.Sprintf("cd %s && %s", arguments.WorkingDirectory, arguments.Command)
  }
  // Shell commands are arbitrary code execution: never auto-execute.
  return &sgptpb.ToolCallMetadata{
    DisplayMessage: &sgptpb.DisplayMessage{Content: display},
  }, nil
}

func (t *ShellTool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
  arguments, err := parseShellCommandArguments(toolCall)
  if err != nil {
    return nil, err
  }
  command := exec.Command("sh", "-c", arguments.Command)
  if arguments.WorkingDirectory != "" {
    command.Dir = arguments.WorkingDirectory
  }
  output, err := command.CombinedOutput()
  content := string(output)
  if err != nil {
    // Surface failures as content so the model can react to them.
    content = fmt.Sprintf("Command failed with error: %v\nOutput: %s", err, string(output))
  }
  return &aipb.ToolResult{
    ToolName:   toolCall.Name,
    ToolCallId: toolCall.Id,
    Result:     &aipb.ToolResult_Content{Content: content},
  }, nil
}

var _ Tool = (*ShellTool)(nil)
EOF

cat > internal/tools/read_files.go <<'EOF'
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

// ReadFiles is the tool definition for file reading.
var ReadFiles = &aipb.Tool{
  Name:        "read_files",
  Description: "Read the contents of one or more files. Use this to examine file contents before making changes or to understand code structure.",
  JsonSchema: &jsonpb.Schema{
    Type: "object",
    Properties: map[string]*jsonpb.Schema{
      "paths": {
        Type:        "array",
        Description: "List of file paths to read",
        Items:       &jsonpb.Schema{Type: "string"},
      },
    },
    Required: []string{"paths"},
  },
  Annotations: map[string]string{
    ToolHandlerIDAnnotation: HandlerIDReadFiles,
  },
}

type readFilesArguments struct {
  Paths []string `json:"paths"`
}

func parseReadFilesArguments(toolCall *aipb.ToolCall) (*readFilesArguments, error) {
  bytes, err := toolCallArgumentsJSON(toolCall)
  if err != nil {
    return nil, err
  }
  arguments := &readFilesArguments{}
  if err := json.Unmarshal(bytes, arguments); err != nil {
    return nil, fmt.Errorf("parsing tool arguments: %w", err)
  }
  if len(arguments.Paths) == 0 {
    return nil, fmt.Errorf("no paths specified")
  }
  return arguments, nil
}

// ReadFilesTool reads files from the user's system.
type ReadFilesTool struct{}

func (t *ReadFilesTool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
  arguments, err := parseReadFilesArguments(toolCall)
  if err != nil {
    return nil, err
  }
  // Reads have no side effects: safe to auto-execute.
  return &sgptpb.ToolCallMetadata{
    DisplayMessage: &sgptpb.DisplayMessage{
      Content: fmt.Sprintf("Reading %d file(s): %s", len(arguments.Paths), strings.Join(arguments.Paths, ", ")),
    },
    AutoExecute: true,
  }, nil
}

func (t *ReadFilesTool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
  arguments, err := parseReadFilesArguments(toolCall)
  if err != nil {
    return nil, err
  }
  results := make([]string, 0, len(arguments.Paths))
  for _, path := range arguments.Paths {
    content, err := os.ReadFile(path)
    if err != nil {
      results = append(results, fmt.Sprintf("=== %s ===\nError: %v", path, err))
      continue
    }
    results = append(results, fmt.Sprintf("=== %s ===\n%s", path, string(content)))
  }
  return &aipb.ToolResult{
    ToolName:   toolCall.Name,
    ToolCallId: toolCall.Id,
    Result:     &aipb.ToolResult_Content{Content: strings.Join(results, "\n\n")},
  }, nil
}

var _ Tool = (*ReadFilesTool)(nil)
EOF

cat > internal/tools/BUILD.plz <<'EOF'
go_library(
    name = "tools",
    srcs = [
        "read_files.go",
        "registry.go",
        "shell.go",
        "tools.go",
    ],
    visibility = ["//..."],
    deps = [
        "//sgpt/v1",
        "//third_party/go:github.com__malonaz__core__go__pbutil",
        "//third_party/proto:malonaz__core__genproto__ai__v1",
        "//third_party/proto:malonaz__core__genproto__json__v1",
    ],
)
EOF

############################################
# internal/toolengine — implements tools.Tool
############################################
cat > internal/toolengine/toolengine.go <<'EOF'
package toolengine

import (
  "context"
  "fmt"
  "strings"
  "sync"
  "time"

  aienginepb "github.com/malonaz/core/genproto/ai/ai_engine/v1"
  aipb "github.com/malonaz/core/genproto/ai/v1"
  "github.com/malonaz/core/go/ai"
  aitool "github.com/malonaz/core/go/ai/tool"
  "github.com/malonaz/core/go/aip"
  "github.com/malonaz/core/go/grpc"
  "github.com/malonaz/core/go/grpc/middleware"
  "github.com/malonaz/core/go/pbutil"
  "github.com/malonaz/core/go/pbutil/pbfieldmask"
  "github.com/malonaz/core/go/pbutil/pbjson"
  "github.com/malonaz/core/go/pbutil/pbreflection"
  reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
  "google.golang.org/protobuf/reflect/protoreflect"
  descriptorpb "google.golang.org/protobuf/types/descriptorpb"
  "google.golang.org/protobuf/types/dynamicpb"
  "google.golang.org/protobuf/types/known/structpb"

  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
  "github.com/malonaz/sgpt/internal/cache"
  "github.com/malonaz/sgpt/internal/debug"
  "github.com/malonaz/sgpt/internal/tools"
)

const (
  toolSetCacheKeyPrefix = "toolset_"
  toolSetCacheMaxAge    = 24 * time.Hour
  schemaCacheMaxAge     = 24 * time.Hour
)

type engineConnection struct {
  client           aienginepb.AiEngineClient
  methodInvoker    *pbreflection.MethodInvoker
  reflectionClient reflectionpb.ServerReflectionClient
  schema           *pbreflection.Schema
  schemaBuilder    *pbjson.SchemaBuilder
}

// Manager connects to remote tool engines and implements tools.Tool for
// the tool sets they expose.
type Manager struct {
  mu                  sync.Mutex
  toolSets            []*aipb.ToolSet
  toolSetNameToEngine map[string]*engineConnection
  closers             []func()
}

func toolSetCacheKey(engineName string, index int) string {
  return fmt.Sprintf("%s%s_%d.pb", toolSetCacheKeyPrefix, engineName, index)
}

func Initialize(
  ctx context.Context,
  config *sgptpb.Configuration,
  baseURLToGRPCConnection map[string]*grpc.Connection,
) (*Manager, error) {
  manager := &Manager{
    toolSetNameToEngine: map[string]*engineConnection{},
  }

  for _, toolEngine := range config.GetToolEngines() {
    connection := baseURLToGRPCConnection[toolEngine.GetEngineService().GetBaseUrl()]
    reflectionClient := reflectionpb.NewServerReflectionClient(connection.Get())

    // Resolve and cache schema per engine.
    schema, err := pbreflection.ResolveSchema(ctx, reflectionClient,
      pbreflection.WithDiskCache(toolEngine.GetEngineService().GetBaseUrl(), cache.Dir(), schemaCacheMaxAge),
    )
    if err != nil {
      return nil, fmt.Errorf("resolving schema for %s: %w", toolEngine.GetName(), err)
    }

    engine := &engineConnection{
      client:           aienginepb.NewAiEngineClient(connection.Get()),
      methodInvoker:    pbreflection.NewMethodInvoker(connection.Get()),
      reflectionClient: reflectionClient,
      schema:           schema,
      schemaBuilder:    pbjson.NewSchemaBuilder(schema),
    }
    for i, request := range toolEngine.GetToolSets() {
      cacheKey := toolSetCacheKey(toolEngine.GetName(), i)

      cachedToolSet, ok := cache.Get(cacheKey, toolSetCacheMaxAge, &aipb.ToolSet{})
      if ok && cachedToolSet.GetName() != "" {
        manager.toolSetNameToEngine[cachedToolSet.GetName()] = engine
        manager.toolSets = append(manager.toolSets, cachedToolSet)
        continue
      }

      toolSet, err := engine.client.CreateServiceToolSet(ctx, request)
      if err != nil {
        return nil, err
      }
      aip.SetAnnotation(toolSet.DiscoveryTool, tools.ToolHandlerIDAnnotation, tools.HandlerIDEngine)
      for _, tool := range toolSet.GetTools() {
        aip.SetAnnotation(tool, tools.ToolHandlerIDAnnotation, tools.HandlerIDEngine)
      }
      cache.Store(cacheKey, toolSet)
      manager.toolSetNameToEngine[toolSet.GetName()] = engine
      manager.toolSets = append(manager.toolSets, toolSet)
    }
  }
  return manager, nil
}

// GetToolSets returns the tool sets exposed by all connected engines.
func (m *Manager) GetToolSets() []*aipb.ToolSet {
  if m == nil {
    return nil
  }
  return m.toolSets
}

func (m *Manager) engineFor(toolCall *aipb.ToolCall) (*engineConnection, error) {
  toolSetName, ok := aip.GetAnnotation(toolCall, aitool.AnnotationKeyToolSetName)
  if !ok {
    return nil, fmt.Errorf("no tool set annotation found on tool call")
  }
  engine, ok := m.toolSetNameToEngine[toolSetName]
  if !ok {
    return nil, fmt.Errorf("no engine found for tool set %q", toolSetName)
  }
  return engine, nil
}

// Review implements tools.Tool.
func (m *Manager) Review(ctx context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
  engine, err := m.engineFor(toolCall)
  if err != nil {
    return nil, err
  }

  toolCallMetadata := &sgptpb.ToolCallMetadata{
    DisplayMessage: &sgptpb.DisplayMessage{},
  }

  toolType, _ := aip.GetAnnotation(toolCall, aitool.AnnotationKeyToolType)
  switch toolType {
  case aitool.AnnotationValueToolTypeDiscovery:
    toolResult := toolCall.GetResult()
    if toolResult == nil {
      return nil, fmt.Errorf("discovery tool call %q has no result", toolCall.GetName())
    }
    var displayToolNames []string
    if discovered, ok := aip.GetAnnotation(toolResult, aitool.AnnotationKeyDiscoveredTools); ok && discovered != "" {
      displayToolNames = strings.Split(discovered, ",")
    }
    displayContent := "`●` Discovered tools"
    if len(displayToolNames) > 0 {
      displayContent = fmt.Sprintf("`●` Discovered %s", strings.Join(displayToolNames, ", "))
    }
    parsedResult, err := ai.ParseToolResult(toolResult)
    if err != nil {
      return nil, fmt.Errorf("parsing discovery tool result: %w", err)
    }
    if toolResult.GetError() != nil {
      displayContent += fmt.Sprintf(" (errors: %s)", parsedResult)
    }
    toolCallMetadata.DisplayMessage.Content = displayContent
    toolCallMetadata.AutoExecute = true

  case aitool.AnnotationValueToolTypeGenerateRPCRequest:
    parseToolCallResponse, err := aitool.ParseToolCall(engine.schemaBuilder, toolCall, m.toolSets)
    if err != nil {
      return nil, err
    }
    rpc := parseToolCallResponse.GetRpc()
    descriptor, err := engine.schema.FindDescriptorByName(protoreflect.FullName(rpc.MethodFullName))
    if err != nil {
      return nil, fmt.Errorf("finding descriptor %q: %w", rpc.MethodFullName, err)
    }
    methodDescriptor, ok := descriptor.(protoreflect.MethodDescriptor)
    if !ok {
      return nil, fmt.Errorf("expected method descriptor, got %T", descriptor)
    }
    methodOptions, ok := methodDescriptor.Options().(*descriptorpb.MethodOptions)
    if !ok {
      return nil, fmt.Errorf("expected method options for %q, got %T", rpc.MethodFullName, methodDescriptor.Options())
    }
    // Side-effect-free RPCs are safe to run without user confirmation.
    toolCallMetadata.AutoExecute = methodOptions.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
  default:
    return nil, fmt.Errorf("unknown tool type: %s", toolType)
  }

  debug.LogProto("tool call metadata", toolCallMetadata)
  return toolCallMetadata, nil
}

// Execute implements tools.Tool.
func (m *Manager) Execute(ctx context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
  engine, err := m.engineFor(toolCall)
  if err != nil {
    return nil, err
  }

  parseToolCallResponse, err := aitool.ParseToolCall(engine.schemaBuilder, toolCall, m.toolSets)
  if err != nil {
    return nil, err
  }

  rpc := parseToolCallResponse.GetRpc()
  if rpc == nil {
    return nil, fmt.Errorf("expected RPC parse result, got %T", parseToolCallResponse.Result)
  }

  descriptor, err := engine.schema.FindDescriptorByName(protoreflect.FullName(rpc.MethodFullName))
  if err != nil {
    return nil, fmt.Errorf("finding method descriptor %q: %w", rpc.MethodFullName, err)
  }
  methodDescriptor, ok := descriptor.(protoreflect.MethodDescriptor)
  if !ok {
    return nil, fmt.Errorf("expected method descriptor for %q, got %T", rpc.MethodFullName, descriptor)
  }

  request := dynamicpb.NewMessage(methodDescriptor.Input())
  requestBytes, err := rpc.Request.MarshalJSON()
  if err != nil {
    return nil, fmt.Errorf("marshaling request: %w", err)
  }
  if err := pbutil.JSONUnmarshal(requestBytes, request); err != nil {
    return nil, fmt.Errorf("unmarshaling request: %w", err)
  }

  ctxInvoke := ctx
  if rpc.GetReadMask() != nil {
    ctxInvoke = middleware.WithReadMaskStrict(ctxInvoke, pbfieldmask.New(rpc.GetReadMask()).String())
  }
  response, err := engine.methodInvoker.Invoke(ctxInvoke, methodDescriptor, request)
  if err != nil {
    return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err), nil
  }

  responseBytes, err := pbutil.JSONMarshal(response)
  if err != nil {
    return nil, fmt.Errorf("marshaling response: %w", err)
  }
  value := &structpb.Value{}
  if err := value.UnmarshalJSON(responseBytes); err != nil {
    return nil, fmt.Errorf("unmarshaling response into structpb.Value: %w", err)
  }
  return ai.NewStructuredToolResult(toolCall.Name, toolCall.Id, value), nil
}

// Close tears down all engine connections.
func (m *Manager) Close() {
  for _, closer := range m.closers {
    closer()
  }
  m.closers = nil
}

var _ tools.Tool = (*Manager)(nil)
EOF

############################################
# internal/session — domain state machine
############################################
cat > internal/session/events.go <<'EOF'
package session

// Event is emitted by Session to notify the TUI of state changes.
type Event interface{ sessionEvent() }

// RefreshEvent signals the TUI should re-render from current session state.
type RefreshEvent struct{}

func (RefreshEvent) sessionEvent() {}

// ErrorEvent signals a non-fatal error that should be shown as an alert.
type ErrorEvent struct {
  Err error
}

func (ErrorEvent) sessionEvent() {}
EOF

cat > internal/session/session.go <<'EOF'
// Package session owns the chat lifecycle: streaming, tool handling and
// persistence (via the store). All methods that mutate state are blocking and
// sequential; the TUI drives the session from tea.Cmd goroutines.
package session

import (
  "context"
  "fmt"
  "sync"

  aipb "github.com/malonaz/core/genproto/ai/v1"
  "github.com/malonaz/core/go/ai"
  spb "google.golang.org/genproto/googleapis/rpc/status"
  "google.golang.org/grpc/status"

  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
  "github.com/malonaz/sgpt/internal/store"
  "github.com/malonaz/sgpt/internal/tools"
)

// Params bundles per-chat parameters for a session.
type Params struct {
  Model              *aipb.Model
  Role               *sgptpb.Role
  MaxTokens          int32
  Temperature        float64
  ReasoningEffort    aipb.ReasoningEffort
  Tools              []string
  Chat               string
  AdditionalMessages []*aipb.Message
  InjectedFiles      []string
}

// Session drives a single chat conversation.
type Session struct {
  ctx      context.Context
  params   Params
  store    *store.Store
  registry *tools.Registry

  mu               sync.Mutex
  chat             *sgptpb.Chat
  streamingMessage *aipb.Message
  streamError      error
  streaming        bool
  cancelStream     context.CancelFunc

  totalModelUsage *aipb.ModelUsage
  lastModelUsage  *aipb.ModelUsage

  eventCh chan Event
}

func New(
  ctx context.Context,
  chatStore *store.Store,
  registry *tools.Registry,
  chat *sgptpb.Chat,
  params Params,
) *Session {
  return &Session{
    ctx:             ctx,
    params:          params,
    store:           chatStore,
    registry:        registry,
    chat:            chat,
    totalModelUsage: &aipb.ModelUsage{},
    lastModelUsage:  &aipb.ModelUsage{},
    eventCh:         make(chan Event, 64),
  }
}

func (s *Session) Events() <-chan Event {
  return s.eventCh
}

func (s *Session) emit(event Event) {
  select {
  case s.eventCh <- event:
  default:
  }
}

func (s *Session) refresh() {
  s.emit(RefreshEvent{})
}

func (s *Session) emitError(err error) {
  // Errors must always reach the TUI; block until delivered.
  s.eventCh <- ErrorEvent{Err: err}
}

func (s *Session) Chat() *sgptpb.Chat {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.chat
}

func (s *Session) StreamingMessage() *aipb.Message {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.streamingMessage
}

func (s *Session) StreamError() error {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.streamError
}

func (s *Session) IsStreaming() bool {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.streaming
}

func (s *Session) Params() Params {
  return s.params
}

func (s *Session) TotalModelUsage() *aipb.ModelUsage {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.totalModelUsage
}

func (s *Session) LastModelUsage() *aipb.ModelUsage {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.lastModelUsage
}

func (s *Session) SetReasoningEffort(effort aipb.ReasoningEffort) {
  s.params.ReasoningEffort = effort
}

func (s *Session) PendingToolCalls() []*aipb.ToolCall {
  s.mu.Lock()
  defer s.mu.Unlock()
  return s.pendingToolCallsLocked()
}

func (s *Session) pendingToolCallsLocked() []*aipb.ToolCall {
  messages := s.chat.GetMetadata().GetMessages()
  for i := len(messages) - 1; i >= 0; i-- {
    message := messages[i].GetMessage()
    if message.GetRole() != aipb.Role_ROLE_ASSISTANT {
      continue
    }
    var pending []*aipb.ToolCall
    for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall) {
      if tools.GetToolCallStatus(block.GetToolCall()) == tools.ToolCallStatusPending {
        pending = append(pending, block.GetToolCall())
      }
    }
    return pending
  }
  return nil
}

func (s *Session) SendMessage(text string) {
  userMessage := ai.NewUserMessage(ai.NewTextBlock(text))

  s.mu.Lock()
  s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: userMessage})
  s.streaming = true
  s.streamError = nil
  s.mu.Unlock()

  s.refresh()
  s.runTurn()
}

func (s *Session) CancelStream() {
  s.mu.Lock()
  defer s.mu.Unlock()
  if s.cancelStream != nil {
    s.cancelStream()
  }
}

// runTurn executes a complete turn: stream → process tool calls → save.
// Auto-execute tool calls are handled immediately. Non-auto ones pause for user.
// Loops if all tool calls in a turn were auto-executed.
func (s *Session) runTurn() {
  for {
    blocks, err := s.stream()

    s.mu.Lock()
    ai.AggregateModelUsage(s.totalModelUsage, s.lastModelUsage)
    *s.lastModelUsage = aipb.ModelUsage{}
    s.mu.Unlock()

    if err != nil {
      s.refresh()
      return
    }

    var toolCalls []*aipb.ToolCall
    for _, block := range ai.FilterBlocks(blocks, ai.BlockTypeToolCall) {
      toolCalls = append(toolCalls, block.GetToolCall())
    }

    if len(toolCalls) == 0 {
      if err := s.saveChat(); err != nil {
        s.emitError(fmt.Errorf("saving chat: %w", err))
      }
      s.refresh()
      return
    }

    allAutoExecuted, err := s.processToolCallsAfterStream(toolCalls)
    if err != nil {
      s.emitError(fmt.Errorf("processing tool calls: %w", err))
      s.refresh()
      return
    }

    if !allAutoExecuted {
      // Manual tool calls remain pending for user accept/reject.
      s.refresh()
      return
    }

    // All auto-executed — loop to stream again with tool results.
    s.mu.Lock()
    s.streaming = true
    s.mu.Unlock()
  }
}

func (s *Session) messagesForAPI() []*aipb.Message {
  s.mu.Lock()
  defer s.mu.Unlock()

  messages := make([]*aipb.Message, 0, len(s.params.AdditionalMessages)+len(s.chat.Metadata.Messages))
  messages = append(messages, s.params.AdditionalMessages...)
  for _, chatMessage := range s.chat.Metadata.Messages {
    if chatMessage.Error == nil {
      messages = append(messages, chatMessage.Message)
    }
  }
  return messages
}

func (s *Session) saveChat() error {
  s.mu.Lock()
  defer s.mu.Unlock()

  if s.chat.GetName() == "" {
    chat, err := s.store.CreateChat(s.ctx, s.chat)
    if err != nil {
      return err
    }
    s.chat = chat
    return nil
  }

  chat, err := s.store.UpdateChat(s.ctx, s.chat, "tags", "files", "metadata")
  if err != nil {
    return err
  }
  s.chat = chat
  return nil
}

// ToggleFavorite flips the favorite tag on the chat and persists.
// Returns true if the chat is now a favorite.
func (s *Session) ToggleFavorite() bool {
  s.mu.Lock()
  favorite := !store.HasTag(s.chat, store.FavoriteTag)
  store.SetTag(s.chat, store.FavoriteTag, favorite)
  s.mu.Unlock()

  if err := s.saveChat(); err != nil {
    s.emitError(fmt.Errorf("saving favorite: %w", err))
  }
  return favorite
}

func statusToProto(err error) *spb.Status {
  if err == nil {
    return nil
  }
  return status.Convert(err).Proto()
}
EOF

cat > internal/session/stream.go <<'EOF'
package session

import (
  "context"
  "fmt"
  "io"
  "time"

  aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
  aipb "github.com/malonaz/core/genproto/ai/v1"
  "github.com/malonaz/core/go/ai"
  "google.golang.org/protobuf/proto"

  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
  "github.com/malonaz/sgpt/internal/debug"
  "github.com/malonaz/sgpt/internal/tools"
)

const renderThrottleInterval = 66 * time.Millisecond

// stream runs a single streaming request to the AI provider. Blocks until the
// stream completes or errors. Returns the finalized blocks.
func (s *Session) stream() ([]*aipb.Block, error) {
  streamCtx, cancel := context.WithCancel(s.ctx)
  defer cancel()

  s.mu.Lock()
  s.cancelStream = cancel
  s.mu.Unlock()

  textToTextStreamRequest := &aiservicepb.TextToTextStreamRequest{
    Model:    s.params.Model.Name,
    Messages: s.messagesForAPI(),
    Tools:    s.registry.Tools(),
    ToolSets: s.registry.ToolSets(),
    Configuration: &aiservicepb.TextToTextConfiguration{
      MaxTokens:       s.params.MaxTokens,
      Temperature:     s.params.Temperature,
      ReasoningEffort: s.params.ReasoningEffort,
    },
  }
  debug.LogProto("request", textToTextStreamRequest, "messages", "tools")
  stream, err := s.store.TextToTextStream(streamCtx, textToTextStreamRequest)
  if err != nil {
    s.finalizeStream(nil, err)
    return nil, fmt.Errorf("opening stream: %w", err)
  }

  accumulator := ai.NewTextToTextAccumulator()
  lastRender := time.Now()
  pendingRender := false
  reviewedToolCallCount := 0

  checkRender := func() {
    if time.Since(lastRender) >= renderThrottleInterval {
      s.refresh()
      lastRender = time.Now()
      pendingRender = false
    } else {
      pendingRender = true
    }
  }

  for {
    select {
    case <-streamCtx.Done():
      if pendingRender {
        s.refresh()
      }
      s.finalizeStream(accumulator.Message.GetBlocks(), streamCtx.Err())
      return nil, fmt.Errorf("stream cancelled: %w", streamCtx.Err())
    default:
    }

    response, err := stream.Recv()
    if err != nil {
      if pendingRender {
        s.refresh()
      }
      if err == io.EOF {
        blocks := accumulator.Message.GetBlocks()
        s.finalizeStream(blocks, nil)
        return blocks, nil
      }
      s.finalizeStream(accumulator.Message.GetBlocks(), err)
      return nil, fmt.Errorf("receiving stream: %w", err)
    }
    debug.LogProto("response", response)

    if err := accumulator.Add(response); err != nil {
      if pendingRender {
        s.refresh()
      }
      s.finalizeStream(accumulator.Message.GetBlocks(), err)
      return nil, fmt.Errorf("accumulating stream response: %w", err)
    }

    s.mu.Lock()
    s.streamingMessage = accumulator.Message
    s.mu.Unlock()

    if modelUsage := response.GetModelUsage(); modelUsage != nil {
      s.mu.Lock()
      proto.Merge(s.lastModelUsage, modelUsage)
      s.mu.Unlock()
    }

    // Review new tool calls eagerly as they arrive during streaming.
    toolCallBlocks := ai.FilterBlocks(accumulator.Message.GetBlocks(), ai.BlockTypeToolCall)
    for len(toolCallBlocks) > reviewedToolCallCount {
      s.reviewToolCallEagerly(toolCallBlocks[reviewedToolCallCount].GetToolCall())
      reviewedToolCallCount++
    }

    checkRender()
  }
}

// reviewToolCallEagerly attaches display/auto-execute metadata to a tool call
// as soon as it appears in the stream, without waiting for stream completion.
func (s *Session) reviewToolCallEagerly(toolCall *aipb.ToolCall) {
  debug.LogProto("eager", toolCall)
  if !s.registry.Handles(toolCall) {
    return
  }
  if _, err := s.registry.Review(s.ctx, toolCall); err != nil {
    s.emitError(fmt.Errorf("reviewing tool call %q: %w", toolCall.Name, err))
  }
}

// finalizeStream commits the streamed message to the chat and resets stream state.
func (s *Session) finalizeStream(blocks []*aipb.Block, err error) {
  s.mu.Lock()
  defer s.mu.Unlock()

  // Persist even if no tokens arrived — gRPC stream errors typically surface
  // on the first Recv(), when streamingMessage is still nil. Without this,
  // the error would only live in the ephemeral streamError and could vanish.
  if s.streamingMessage != nil || err != nil {
    assistantMessage := ai.NewAssistantMessage(blocks...)

    for _, block := range ai.FilterBlocks(blocks, ai.BlockTypeToolCall) {
      if block.GetToolCall().GetResult() != nil {
        tools.SetToolCallStatus(block.GetToolCall(), tools.ToolCallStatusAccepted)
      } else {
        tools.SetToolCallStatus(block.GetToolCall(), tools.ToolCallStatusPending)
      }
    }

    chatMessage := &sgptpb.Message{
      Message: assistantMessage,
    }
    if err != nil {
      chatMessage.Error = statusToProto(err)
    }
    s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, chatMessage)
  }

  s.streamingMessage = nil
  s.streaming = false
  s.cancelStream = nil
  s.streamError = err
}
EOF

cat > internal/session/tools.go <<'EOF'
package session

import (
  "fmt"

  aipb "github.com/malonaz/core/genproto/ai/v1"
  "github.com/malonaz/core/go/ai"

  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
  "github.com/malonaz/sgpt/internal/debug"
  "github.com/malonaz/sgpt/internal/tools"
)

// processToolCallsAfterStream handles tool calls after the stream completes.
// Auto-execute tool calls are run immediately. Non-auto ones are left pending
// for user accept/reject. Returns true if all tool calls were auto-executed.
func (s *Session) processToolCallsAfterStream(toolCalls []*aipb.ToolCall) (bool, error) {
  var autoToolCalls []*aipb.ToolCall
  var prePopulatedToolCalls []*aipb.ToolCall
  hasManual := false
  for _, toolCall := range toolCalls {
    debug.LogProto(toolCall.GetName(), toolCall)
    if toolCall.GetResult() != nil {
      prePopulatedToolCalls = append(prePopulatedToolCalls, toolCall)
      continue
    }
    metadata, err := tools.ParseToolCallMetadata(toolCall)
    if err != nil {
      return false, fmt.Errorf("parsing tool call metadata: %w", err)
    }
    if metadata.GetAutoExecute() {
      autoToolCalls = append(autoToolCalls, toolCall)
    } else {
      hasManual = true
    }
  }

  s.mu.Lock()
  for _, toolCall := range autoToolCalls {
    tools.SetToolCallStatus(toolCall, tools.ToolCallStatusAccepted)
  }
  s.mu.Unlock()

  if hasManual {
    // Manual calls stay pending; pre-accepted auto calls execute alongside
    // them once the user resolves the pending ones.
    return false, nil
  }

  s.executeToolCalls(append(prePopulatedToolCalls, autoToolCalls...))
  return true, nil
}

// ResolveToolCalls produces a single tool message with results for ALL tool
// calls of the last assistant message: accepted ones are executed, rejected
// ones get error results. Then starts a new turn.
func (s *Session) ResolveToolCalls() {
  s.mu.Lock()
  messages := s.chat.GetMetadata().GetMessages()
  var toolCalls []*aipb.ToolCall
  for i := len(messages) - 1; i >= 0; i-- {
    message := messages[i].GetMessage()
    if message.GetRole() != aipb.Role_ROLE_ASSISTANT {
      continue
    }
    for _, block := range ai.FilterBlocks(message.GetBlocks(), ai.BlockTypeToolCall) {
      toolCalls = append(toolCalls, block.GetToolCall())
    }
    break
  }
  s.mu.Unlock()

  resultBlocks := make([]*aipb.Block, 0, len(toolCalls))
  for _, toolCall := range toolCalls {
    resultBlocks = append(resultBlocks, ai.NewToolResultBlock(s.resolveToolCall(toolCall)))
  }
  s.appendToolMessage(resultBlocks)
  s.refresh()

  s.mu.Lock()
  s.streaming = true
  s.mu.Unlock()

  s.runTurn()
}

// resolveToolCall produces a result for a reviewed tool call based on its status.
func (s *Session) resolveToolCall(toolCall *aipb.ToolCall) *aipb.ToolResult {
  if toolCall.GetResult() != nil {
    return toolCall.GetResult()
  }
  switch tools.GetToolCallStatus(toolCall) {
  case tools.ToolCallStatusRejected:
    reason := tools.GetToolCallRejectionReason(toolCall)
    return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("rejected by user: %s", reason))
  case tools.ToolCallStatusAccepted:
    toolResult, err := s.registry.Execute(s.ctx, toolCall)
    if err != nil {
      return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
    }
    return toolResult
  default:
    return ai.NewErrorToolResult(toolCall.Name, toolCall.Id, fmt.Errorf("unresolved tool call"))
  }
}

// executeToolCalls executes tool calls and appends a single tool message with
// all results. Used for fully-auto turns only.
func (s *Session) executeToolCalls(toolCalls []*aipb.ToolCall) {
  resultBlocks := make([]*aipb.Block, 0, len(toolCalls))
  for _, toolCall := range toolCalls {
    if toolCall.GetResult() != nil {
      resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolCall.GetResult()))
      continue
    }
    toolResult, err := s.registry.Execute(s.ctx, toolCall)
    if err != nil {
      toolResult = ai.NewErrorToolResult(toolCall.Name, toolCall.Id, err)
    }
    resultBlocks = append(resultBlocks, ai.NewToolResultBlock(toolResult))
  }
  s.appendToolMessage(resultBlocks)
  s.refresh()
}

func (s *Session) appendToolMessage(resultBlocks []*aipb.Block) {
  s.mu.Lock()
  defer s.mu.Unlock()
  toolMessage := ai.NewToolMessage(resultBlocks...)
  s.chat.Metadata.Messages = append(s.chat.Metadata.Messages, &sgptpb.Message{Message: toolMessage})
}
EOF

cat > internal/session/BUILD.plz <<'EOF'
go_library(
    name = "session",
    srcs = [
        "events.go",
        "session.go",
        "stream.go",
        "tools.go",
    ],
    visibility = ["//..."],
    deps = [
        "//internal/debug",
        "//internal/store",
        "//internal/tools",
        "//sgpt/v1",
        "//third_party/go:github.com__malonaz__core__go__ai",
        "//third_party/go:google.golang.org__grpc__status",
        "//third_party/go:google.golang.org__protobuf__proto",
        "//third_party/proto:google__rpc__status",
        "//third_party/proto:malonaz__core__genproto__ai__ai_service__v1",
        "//third_party/proto:malonaz__core__genproto__ai__v1",
    ],
)
EOF

############################################
# cli/chat — wiring only
############################################
cat > cli/chat/cmd.go <<'EOF'
package chat

import (
  "context"
  "fmt"
  "strings"
  "time"

  tea "charm.land/bubbletea/v2"
  aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
  aipb "github.com/malonaz/core/genproto/ai/v1"
  "github.com/malonaz/core/go/ai"
  "github.com/malonaz/core/go/grpc"
  "github.com/spf13/cobra"

  "github.com/malonaz/sgpt/cli/tui"
  sgptservicepb "github.com/malonaz/sgpt/genproto/sgpt/sgpt_service/v1"
  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
  "github.com/malonaz/sgpt/internal/debug"
  "github.com/malonaz/sgpt/internal/file"
  "github.com/malonaz/sgpt/internal/role"
  "github.com/malonaz/sgpt/internal/session"
  "github.com/malonaz/sgpt/internal/store"
  "github.com/malonaz/sgpt/internal/toolengine"
  "github.com/malonaz/sgpt/internal/tools"
)

func NewCmd(
  config *sgptpb.Configuration,
  aiClient aiservicepb.AiServiceClient,
  chatClient sgptservicepb.SgptServiceClient,
  baseURLToGRPCConnection map[string]*grpc.Connection,
) *cobra.Command {
  chatStore := store.New(config, aiClient, chatClient)

  var opts struct {
    FileInjection *file.InjectionOpts
    Role          *role.Opts
    Model         string
    MaxTokens     int32
    Temperature   float64
    Chat          string
    Continue      bool
    Tools         []string
    Debug         bool
  }

  cmd := &cobra.Command{
    Use: "chat",
    RunE: func(cmd *cobra.Command, args []string) error {
      ctx, cancel := context.WithTimeout(cmd.Context(), 365*24*time.Hour)
      defer cancel()
      if opts.Debug {
        if _, err := debug.Init(ctx); err != nil {
          return fmt.Errorf("starting debug server: %w", err)
        }
      }

      parsedRole, err := opts.Role.Parse()
      cobra.CheckErr(err)

      if opts.Model == "" {
        if parsedRole != nil && parsedRole.Model != "" {
          opts.Model = parsedRole.Model
        } else {
          opts.Model = config.Chat.DefaultModel
        }
      }
      selectedModel, err := chatStore.ResolveModel(ctx, opts.Model)
      cobra.CheckErr(err)

      opts.FileInjection.Files = append(opts.FileInjection.Files, args...)
      opts.FileInjection.Files = append(opts.FileInjection.Files, parsedRole.GetFiles()...)
      files, err := file.Parse(opts.FileInjection)
      cobra.CheckErr(err)
      filePaths := make([]string, len(files))
      for i, parsedFile := range files {
        filePaths[i] = parsedFile.Path
      }

      // Tag the chat with the GitHub repos its files belong to.
      var tags []string
      githubRepoSet := map[string]struct{}{}
      for _, filePath := range filePaths {
        githubRepo, err := file.GetGitHubRepo(filePath)
        cobra.CheckErr(err)
        githubRepoSet[githubRepo] = struct{}{}
      }
      for githubRepo := range githubRepoSet {
        tags = append(tags, githubRepo)
      }

      registry := tools.NewRegistry()
      registry.Register(tools.HandlerIDShell, &tools.ShellTool{})
      registry.Register(tools.HandlerIDReadFiles, &tools.ReadFilesTool{})

      toolNames := append(opts.Tools, parsedRole.GetTools()...)
      var toolEngineConfigurations []*sgptpb.ToolEngineConfiguration
      if len(toolNames) > 0 {
        toolEngineNameSet := map[string]struct{}{}
        for _, name := range toolNames {
          toolEngineNameSet[name] = struct{}{}
        }
        configuredToolEngineNameSet := map[string]struct{}{}
        for _, toolEngineConfiguration := range config.ToolEngines {
          configuredToolEngineNameSet[toolEngineConfiguration.GetName()] = struct{}{}
        }
        for name := range toolEngineNameSet {
          if _, ok := configuredToolEngineNameSet[name]; !ok {
            return fmt.Errorf("unknown tool engine %q", name)
          }
        }

        filteredConfiguration := *config
        for _, toolEngineConfiguration := range config.ToolEngines {
          if _, ok := toolEngineNameSet[toolEngineConfiguration.GetName()]; ok {
            toolEngineConfigurations = append(toolEngineConfigurations, toolEngineConfiguration)
          }
        }
        filteredConfiguration.ToolEngines = toolEngineConfigurations
        toolEngineManager, err := toolengine.Initialize(ctx, &filteredConfiguration, baseURLToGRPCConnection)
        if err != nil {
          return fmt.Errorf("initializing tool engines: %w", err)
        }
        defer toolEngineManager.Close()
        registry.Register(tools.HandlerIDEngine, toolEngineManager)
        registry.AddToolSets(toolEngineManager.GetToolSets()...)
      }

      var chat *sgptpb.Chat
      switch {
      case opts.Chat != "":
        chat, err = chatStore.GetChat(ctx, opts.Chat)
        cobra.CheckErr(err)
      case opts.Continue:
        chat, err = chatStore.LatestChat(ctx)
        cobra.CheckErr(err)
        opts.Chat = chat.Name
      default:
        chat = &sgptpb.Chat{
          Files: filePaths,
          Tags:  tags,
          Metadata: &sgptpb.ChatMetadata{
            CurrentModel: selectedModel.Name,
          },
        }
      }

      additionalMessages := make([]*aipb.Message, 0, len(files)+len(toolEngineConfigurations)+1)
      additionalMessages = append(additionalMessages, ai.NewSystemMessage(ai.NewTextBlock(parsedRole.Prompt)))
      for _, toolEngineConfiguration := range toolEngineConfigurations {
        additionalMessages = append(additionalMessages, ai.NewUserMessage(ai.NewTextBlock(toolEngineConfiguration.Instructions)))
      }
      for _, parsedFile := range files {
        additionalMessages = append(additionalMessages, ai.NewUserMessage(ai.NewTextBlock(fmt.Sprintf("file %s: `%s`", parsedFile.Path, parsedFile.Content))))
      }

      params := session.Params{
        Model:              selectedModel,
        Role:               parsedRole,
        MaxTokens:          opts.MaxTokens,
        Temperature:        opts.Temperature,
        Chat:               opts.Chat,
        AdditionalMessages: additionalMessages,
        InjectedFiles:      filePaths,
        Tools:              toolNames,
      }

      app := tui.NewApp(ctx, chatStore, registry, chat, params)
      program := tea.NewProgram(app, tea.WithContext(ctx))
      app.SetProgram(program)
      if _, err := program.Run(); err != nil {
        return fmt.Errorf("running chat: %w", err)
      }
      return nil
    },
  }

  opts.FileInjection = file.GetOpts(cmd)
  opts.Role = role.GetOpts(cmd, config.Chat.DefaultRole, config.Chat.Roles)
  cmd.Flags().StringVarP(&opts.Model, "model", "m", "", "Model name or alias")
  cmd.Flags().Int32Var(&opts.MaxTokens, "max-tokens", 0, "Maximum tokens to generate")
  cmd.Flags().Float64Var(&opts.Temperature, "temperature", 0, "Temperature (0.0-2.0)")
  cmd.Flags().StringVar(&opts.Chat, "name", "", "Chat to resume")
  cmd.Flags().BoolVarP(&opts.Continue, "continue", "c", false, "Continue previous chat")
  cmd.Flags().StringSliceVar(&opts.Tools, "tool", nil, "Enable a specific tool engine by name (repeatable)")
  cmd.Flags().BoolVar(&opts.Debug, "debug", false, "Start a local debug log server")

  cmd.RegisterFlagCompletionFunc("model", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    models, _ := chatStore.ListModels(cmd.Context(), false)
    return filterModels(models, toComplete), cobra.ShellCompDirectiveNoFileComp
  })

  cmd.RegisterFlagCompletionFunc("tool", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    var names []string
    for _, toolEngineConfiguration := range config.GetToolEngines() {
      name := toolEngineConfiguration.GetName()
      if toComplete == "" || strings.Contains(strings.ToLower(name), strings.ToLower(toComplete)) {
        names = append(names, name)
      }
    }
    return names, cobra.ShellCompDirectiveNoFileComp
  })

  cmd.RegisterFlagCompletionFunc("role", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    var names []string
    for _, configuredRole := range config.Chat.GetRoles() {
      name := configuredRole.GetName()
      if toComplete == "" || strings.Contains(strings.ToLower(name), strings.ToLower(toComplete)) {
        names = append(names, name)
      }
      if alias := configuredRole.GetAlias(); alias != "" {
        if toComplete == "" || strings.Contains(strings.ToLower(alias), strings.ToLower(toComplete)) {
          names = append(names, alias)
        }
      }
    }
    return names, cobra.ShellCompDirectiveNoFileComp
  })

  return cmd
}

func filterModels(models []*aipb.Model, prefix string) []string {
  var names []string
  for _, model := range models {
    names = append(names, model.Name)
  }
  if prefix == "" {
    return names
  }
  lowerPrefix := strings.ToLower(prefix)
  var matches []string
  for _, name := range names {
    if strings.Contains(strings.ToLower(name), lowerPrefix) {
      matches = append(matches, name)
    }
  }
  return matches
}
EOF

cat > cli/chat/BUILD.plz <<'EOF'
go_library(
    name = "chat",
    srcs = ["cmd.go"],
    visibility = ["//..."],
    deps = [
        "//cli/tui",
        "//internal/debug",
        "//internal/file",
        "//internal/role",
        "//internal/session",
        "//internal/store",
        "//internal/toolengine",
        "//internal/tools",
        "//sgpt/sgpt_service/v1",
        "//sgpt/v1",
        "//third_party/go:charm.land__bubbletea__v2",
        "//third_party/go:github.com__malonaz__core__go__ai",
        "//third_party/go:github.com__malonaz__core__go__grpc",
        "//third_party/go:github.com__spf13__cobra",
        "//third_party/proto:malonaz__core__genproto__ai__ai_service__v1",
        "//third_party/proto:malonaz__core__genproto__ai__v1",
    ],
)
EOF

############################################
# cli/tui — app no longer performs RPCs directly
############################################
cat > cli/tui/app.go <<'EOF'
package tui

import (
  "context"
  "fmt"
  "strings"
  "time"

  "charm.land/bubbles/v2/key"
  tea "charm.land/bubbletea/v2"
  "charm.land/lipgloss/v2"
  "golang.design/x/clipboard"

  "github.com/malonaz/sgpt/cli/tui/screen"
  menuscreen "github.com/malonaz/sgpt/cli/tui/screen/menu"
  "github.com/malonaz/sgpt/cli/tui/styles"
  "github.com/malonaz/sgpt/cli/tui/widget"
  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
  "github.com/malonaz/sgpt/internal/session"
  "github.com/malonaz/sgpt/internal/store"
  "github.com/malonaz/sgpt/internal/tools"
)

const alertDuration = 2 * time.Second
const menuTabID = "menu"

type alertDismissMsg struct{}

type openTabMsg struct {
  id     string
  screen screen.Screen
}

type tab struct {
  id     string
  screen screen.Screen
}

var (
  keyQuit     = key.NewBinding(key.WithKeys("ctrl+c"))
  keyNewTab   = key.NewBinding(key.WithKeys("ctrl+t"))
  keyCloseTab = key.NewBinding(key.WithKeys("ctrl+w"))
  keyPrevTab  = key.NewBinding(key.WithKeys("alt+j"))
  keyNextTab  = key.NewBinding(key.WithKeys("alt+;"))
  keyOpenMenu = key.NewBinding(key.WithKeys("alt+m"))
  keySearch   = key.NewBinding(key.WithKeys("ctrl+_"))
  keyCopyName = key.NewBinding(key.WithKeys("alt+c"))
  keyTab1     = key.NewBinding(key.WithKeys("alt+f1"))
  keyTab2     = key.NewBinding(key.WithKeys("alt+f2"))
  keyTab3     = key.NewBinding(key.WithKeys("alt+f3"))
  keyTab4     = key.NewBinding(key.WithKeys("alt+f4"))
  keyTab5     = key.NewBinding(key.WithKeys("alt+f5"))
  keyTab6     = key.NewBinding(key.WithKeys("alt+f6"))
  keyTab7     = key.NewBinding(key.WithKeys("alt+f7"))
  keyTab8     = key.NewBinding(key.WithKeys("alt+f8"))
  keyTab9     = key.NewBinding(key.WithKeys("alt+f9"))
)

var tabIndexKeys = []key.Binding{keyTab1, keyTab2, keyTab3, keyTab4, keyTab5, keyTab6, keyTab7, keyTab8, keyTab9}

type App struct {
  ctx      context.Context
  store    *store.Store
  registry *tools.Registry

  defaultParams session.Params

  tabs      []*tab
  activeTab int

  program *tea.Program
  width   int
  height  int
  ready   bool

  // Alerts are queued so that none are ever lost; they display one at a
  // time, each for alertDuration.
  alertQueue   []string
  alertVisible bool
  quitting     bool
}

func NewApp(
  ctx context.Context,
  chatStore *store.Store,
  registry *tools.Registry,
  initialChat *sgptpb.Chat,
  params session.Params,
) *App {
  app := &App{
    ctx:           ctx,
    store:         chatStore,
    registry:      registry,
    defaultParams: params,
  }

  menuScreen := menuscreen.New(ctx, chatStore, app.makeWrap(menuTabID))

  tabID := params.Chat
  chatSession := session.New(ctx, chatStore, registry, initialChat, params)
  chatScreen := screen.NewChatScreen(app.makeWrap(tabID), app.makeSend(tabID), chatSession, params.InjectedFiles)

  app.tabs = []*tab{
    {id: menuTabID, screen: menuScreen},
    {id: tabID, screen: chatScreen},
  }
  app.activeTab = 1
  return app
}

func (a *App) SetProgram(p *tea.Program) {
  a.program = p
}

func (a *App) Init() tea.Cmd {
  var cmds []tea.Cmd
  for i, t := range a.tabs {
    cmds = append(cmds, t.screen.Init())
    if i == a.activeTab {
      cmds = append(cmds, t.screen.OnFocus())
    }
  }
  return tea.Batch(cmds...)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
  switch msg := msg.(type) {
  case alertDismissMsg:
    // Drop the alert that just finished displaying, then show the next.
    if len(a.alertQueue) > 0 {
      a.alertQueue = a.alertQueue[1:]
    }
    return a, a.displayNextAlert()

  case openTabMsg:
    cmd := a.addTab(msg.id, msg.screen)
    return a, cmd

  case tea.WindowSizeMsg:
    a.width = msg.Width
    a.height = msg.Height
    a.ready = true
    contentHeight := a.contentHeight()
    for _, t := range a.tabs {
      t.screen.SetSize(a.width, contentHeight)
    }
    return a, nil

  case screen.TabMsg:
    for _, t := range a.tabs {
      if t.id == msg.TabID {
        switch innerMsg := msg.Msg.(type) {
        case screen.AlertMsg:
          return a, a.showAlert(innerMsg.Text)
        case screen.OpenChatMsg:
          return a, a.openChat(innerMsg)
        case screen.CloseTabMsg:
          return a, a.closeTab(msg.TabID)
        default:
          cmd := t.screen.Update(innerMsg)
          return a, cmd
        }
      }
    }
    return a, nil

  case screen.AlertMsg:
    return a, a.showAlert(msg.Text)

  case screen.OpenChatMsg:
    return a, a.openChat(msg)

  case screen.OpenMenuMsg:
    return a, a.focusMenu()

  case screen.OpenSearchMsg:
    return a, a.focusMenuSearch()

  case screen.CloseTabMsg:
    return a, a.closeTab(msg.TabID)

  case tea.KeyPressMsg:
    if cmd := a.handleGlobalKey(msg); cmd != nil {
      return a, cmd
    }
  }

  if a.activeTab < len(a.tabs) {
    cmd := a.tabs[a.activeTab].screen.Update(msg)
    return a, cmd
  }
  return a, nil
}

func (a *App) View() tea.View {
  if a.quitting {
    return tea.NewView("")
  }
  if !a.ready {
    return tea.NewView("Initializing...")
  }

  var b strings.Builder
  if a.alertVisible && len(a.alertQueue) > 0 {
    alertStyle := lipgloss.NewStyle().
      Background(styles.SuccessColor).
      Foreground(lipgloss.Color("#000000")).
      Bold(true).
      Padding(0, 1)
    b.WriteString(alertStyle.Width(a.width).Render(a.alertQueue[0]))
  } else {
    b.WriteString(a.renderTabBar())
  }
  b.WriteString("\n")
  if a.activeTab < len(a.tabs) {
    b.WriteString(a.tabs[a.activeTab].screen.View())
  }

  view := tea.NewView(b.String())
  view.AltScreen = true
  view.ReportFocus = true
  return view
}

func (a *App) handleGlobalKey(msg tea.KeyPressMsg) tea.Cmd {
  switch {
  case key.Matches(msg, keyQuit):
    if a.activeTab < len(a.tabs) {
      if chatScreen, ok := a.tabs[a.activeTab].screen.(*screen.ChatScreen); ok && chatScreen.IsStreaming() {
        break
      }
    }
    a.quitting = true
    return tea.Quit
  case key.Matches(msg, keyNewTab):
    return a.createNewChat()
  case key.Matches(msg, keyCloseTab):
    return a.closeTab("")
  case key.Matches(msg, keyNextTab):
    return a.switchTab(a.activeTab + 1)
  case key.Matches(msg, keyPrevTab):
    return a.switchTab(a.activeTab - 1)
  case key.Matches(msg, keyOpenMenu):
    return a.focusMenu()
  case key.Matches(msg, keySearch):
    return a.focusMenuSearch()
  case key.Matches(msg, keyCopyName):
    if a.activeTab < len(a.tabs) {
      if chatScreen, ok := a.tabs[a.activeTab].screen.(*screen.ChatScreen); ok {
        chatName := chatScreen.Session().Chat().GetName()
        if chatName != "" {
          clipboard.Write(clipboard.FmtText, []byte(chatName))
          return a.showAlert("Copied chat name: " + chatName)
        }
      }
    }
    return nil
  }
  for i, k := range tabIndexKeys {
    if key.Matches(msg, k) {
      return a.switchTab(i)
    }
  }
  return nil
}

func (a *App) isMenuTab(index int) bool {
  return index >= 0 && index < len(a.tabs) && a.tabs[index].id == menuTabID
}

func (a *App) switchTab(index int) tea.Cmd {
  if index < 0 || index >= len(a.tabs) || index == a.activeTab {
    return nil
  }
  a.tabs[a.activeTab].screen.OnBlur()
  a.activeTab = index
  return a.tabs[a.activeTab].screen.OnFocus()
}

func (a *App) closeTab(tabID string) tea.Cmd {
  removeIndex := a.activeTab
  if tabID != "" {
    for i, t := range a.tabs {
      if t.id == tabID {
        removeIndex = i
        break
      }
    }
  }
  if a.isMenuTab(removeIndex) {
    return nil
  }
  nonMenuTabs := 0
  for _, t := range a.tabs {
    if t.id != menuTabID {
      nonMenuTabs++
    }
  }
  if nonMenuTabs <= 1 {
    a.quitting = true
    return tea.Quit
  }
  a.tabs[removeIndex].screen.OnBlur()
  a.tabs = append(a.tabs[:removeIndex], a.tabs[removeIndex+1:]...)
  if a.activeTab >= len(a.tabs) {
    a.activeTab = len(a.tabs) - 1
  }
  if a.isMenuTab(a.activeTab) && a.activeTab+1 < len(a.tabs) {
    a.activeTab++
  }
  return a.tabs[a.activeTab].screen.OnFocus()
}

func (a *App) addTab(id string, s screen.Screen) tea.Cmd {
  if a.activeTab < len(a.tabs) {
    a.tabs[a.activeTab].screen.OnBlur()
  }
  s.SetSize(a.width, a.contentHeight())
  a.tabs = append(a.tabs, &tab{id: id, screen: s})
  a.activeTab = len(a.tabs) - 1
  return tea.Batch(s.Init(), s.OnFocus())
}

func (a *App) openChat(msg screen.OpenChatMsg) tea.Cmd {
  if !msg.Fork && msg.Chat != nil {
    for i, t := range a.tabs {
      if t.id == msg.Chat.Name {
        return a.switchTab(i)
      }
    }
  }

  return func() tea.Msg {
    chat := msg.Chat
    var err error

    if msg.Fork && chat != nil {
      chat, err = a.store.ForkChat(a.ctx, chat)
      if err != nil {
        return screen.AlertMsg{Text: fmt.Sprintf("Fork failed: %v", err)}
      }
    }

    if chat == nil {
      chat, err = a.store.CreateChat(a.ctx, &sgptpb.Chat{
        Metadata: &sgptpb.ChatMetadata{
          CurrentModel: a.defaultParams.Model.Name,
        },
      })
      if err != nil {
        return screen.AlertMsg{Text: fmt.Sprintf("Create failed: %v", err)}
      }
    }

    params := a.defaultParams
    params.Chat = chat.Name
    tabID := chat.Name

    chatSession := session.New(a.ctx, a.store, a.registry, chat, params)
    chatScreen := screen.NewChatScreen(a.makeWrap(tabID), a.makeSend(tabID), chatSession, params.InjectedFiles)
    return openTabMsg{id: tabID, screen: chatScreen}
  }
}

func (a *App) createNewChat() tea.Cmd {
  return a.openChat(screen.OpenChatMsg{})
}

func (a *App) focusMenu() tea.Cmd {
  for i, t := range a.tabs {
    if t.id == menuTabID {
      return a.switchTab(i)
    }
  }
  return nil
}

func (a *App) focusMenuSearch() tea.Cmd {
  for i, t := range a.tabs {
    if t.id == menuTabID {
      cmd := a.switchTab(i)
      if menuModel, ok := t.screen.(*menuscreen.Model); ok {
        searchCmd := menuModel.ActivateSearch()
        return tea.Batch(cmd, searchCmd)
      }
      return cmd
    }
  }
  return nil
}

func (a *App) showAlert(text string) tea.Cmd {
  a.alertQueue = append(a.alertQueue, text)
  if a.alertVisible {
    // Already displaying; the pending dismiss tick will pop the next one.
    return nil
  }
  return a.displayNextAlert()
}

func (a *App) displayNextAlert() tea.Cmd {
  if len(a.alertQueue) == 0 {
    a.alertVisible = false
    return nil
  }
  a.alertVisible = true
  return tea.Tick(alertDuration, func(time.Time) tea.Msg { return alertDismissMsg{} })
}

func (a *App) makeWrap(tabID string) screen.WrapFunc {
  return func(msg tea.Msg) tea.Msg {
    return screen.TabMsg{TabID: tabID, Msg: msg}
  }
}

func (a *App) makeSend(tabID string) screen.SendFunc {
  return func(msg tea.Msg) {
    if a.program != nil {
      a.program.Send(screen.TabMsg{TabID: tabID, Msg: msg})
    }
  }
}

func (a *App) contentHeight() int {
  if a.height == 0 {
    return 0
  }
  return a.height - lipgloss.Height(a.renderTabBar()) - 1
}

func (a *App) renderTabBar() string {
  var tabs []widget.Tab
  for i, t := range a.tabs {
    streaming := false
    if chatScreen, ok := t.screen.(*screen.ChatScreen); ok {
      streaming = chatScreen.IsStreaming()
    }
    tabs = append(tabs, widget.Tab{
      ID:        t.id,
      Title:     t.screen.ShortTitle(),
      Active:    i == a.activeTab,
      Streaming: streaming,
    })
  }
  return widget.RenderTabBar(tabs, a.width)
}
EOF

cat > cli/tui/BUILD.plz <<'EOF'
go_library(
    name = "tui",
    srcs = ["app.go"],
    visibility = ["//..."],
    deps = [
        "//cli/tui/screen",
        "//cli/tui/screen/menu",
        "//cli/tui/styles",
        "//cli/tui/widget",
        "//internal/session",
        "//internal/store",
        "//internal/tools",
        "//sgpt/v1",
        "//third_party/go:charm.land__bubbles__v2__key",
        "//third_party/go:charm.land__bubbletea__v2",
        "//third_party/go:charm.land__lipgloss__v2",
        "//third_party/go:golang.design__x__clipboard",
    ],
)
EOF

############################################
# cli/tui/screen — chat screen decoupled from cli_service
############################################
cat > cli/tui/screen/chat.go <<'EOF'
package screen

import (
  "fmt"
  "strings"

  "charm.land/bubbles/v2/key"
  "charm.land/bubbles/v2/spinner"
  "charm.land/bubbles/v2/textarea"
  tea "charm.land/bubbletea/v2"
  aipb "github.com/malonaz/core/genproto/ai/v1"

  "github.com/malonaz/sgpt/cli/tui/styles"
  "github.com/malonaz/sgpt/cli/tui/widget"
  "github.com/malonaz/sgpt/internal/session"
)

type FocusedComponent int

const (
  FocusTextarea FocusedComponent = iota
  FocusViewport
)

type sessionEventMsg struct {
  event session.Event
}

var (
  keyCycleFocus     = key.NewBinding(key.WithKeys("tab"))
  keyCycleReasoning = key.NewBinding(key.WithKeys("alt+t"))
  keyForkChat       = key.NewBinding(key.WithKeys("alt+="))
  keyToggleFavorite = key.NewBinding(key.WithKeys("alt+shift+f"))
)

type ChatScreen struct {
  session *session.Session
  wrap    WrapFunc
  send    SendFunc

  titlebar   *widget.TitleBar
  messages   *widget.Messages
  input      *widget.Input
  toolReview *widget.ToolReview
  spinner    spinner.Model

  lastInputHeight int

  injectedFiles []string

  width            int
  height           int
  ready            bool
  focused          bool
  focusedComponent FocusedComponent
}

func NewChatScreen(
  wrap WrapFunc,
  send SendFunc,
  chatSession *session.Session,
  injectedFiles []string,
) *ChatScreen {
  sp := spinner.New()
  sp.Spinner = spinner.Dot
  sp.Style = styles.SpinnerStyle

  cs := &ChatScreen{
    session:          chatSession,
    wrap:             wrap,
    send:             send,
    titlebar:         widget.NewTitleBar(),
    messages:         widget.NewMessages(),
    input:            widget.NewInput(),
    toolReview:       widget.NewToolReview(),
    spinner:          sp,
    injectedFiles:    injectedFiles,
    focusedComponent: FocusTextarea,
  }
  cs.refreshTitle()
  cs.lastInputHeight = cs.input.Height()
  return cs
}

func (m *ChatScreen) Init() tea.Cmd {
  return tea.Batch(textarea.Blink, m.spinner.Tick, m.listenForSessionEvents())
}

func (m *ChatScreen) Title() string {
  name := m.session.Chat().GetName()
  if name == "" {
    return "New Chat"
  }
  return strings.TrimPrefix(name, "chats/")
}

func (m *ChatScreen) ShortTitle() string {
  return styles.Truncate(m.Title(), 20)
}

func (m *ChatScreen) SetSize(width, height int) {
  m.width = width
  m.height = height
  m.recalculateLayout()
}

func (m *ChatScreen) OnFocus() tea.Cmd {
  m.focused = true
  if m.focusedComponent == FocusTextarea && !m.session.IsStreaming() && !m.inToolReview() {
    return m.input.Focus()
  }
  return nil
}

func (m *ChatScreen) OnBlur() {
  m.focused = false
  m.input.Blur()
}

func (m *ChatScreen) IsStreaming() bool {
  return m.session.IsStreaming()
}

func (m *ChatScreen) Session() *session.Session {
  return m.session
}

func (m *ChatScreen) listenForSessionEvents() tea.Cmd {
  eventCh := m.session.Events()
  wrap := m.wrap
  return func() tea.Msg {
    event, ok := <-eventCh
    if !ok {
      return nil
    }
    return wrap(sessionEventMsg{event: event})
  }
}

func (m *ChatScreen) Update(msg tea.Msg) tea.Cmd {
  var cmds []tea.Cmd

  switch msg := msg.(type) {
  case tea.WindowSizeMsg:
    m.width = msg.Width
    m.height = msg.Height
    m.recalculateLayout()
    return nil

  case sessionEventMsg:
    cmds = append(cmds, m.handleSessionEvent(msg.event))
    cmds = append(cmds, m.listenForSessionEvents())
    return tea.Batch(cmds...)

  case widget.EditorClosedMsg:
    switch m.focusedComponent {
    case FocusTextarea:
      if msg.Modified {
        m.input.Textarea.SetValue(msg.Content)
        m.input.AdjustHeight()
      }
      return m.input.Focus()
    case FocusViewport:
      return nil
    }
    return nil

  case spinner.TickMsg:
    var cmd tea.Cmd
    m.spinner, cmd = m.spinner.Update(msg)
    return cmd

  case tea.KeyPressMsg:
    return m.handleKeyPress(msg)
  }

  if !m.session.IsStreaming() && !m.inToolReview() {
    cmd := m.input.Update(msg)
    if m.input.Height() != m.lastInputHeight {
      m.lastInputHeight = m.input.Height()
      m.recalculateLayout()
    }
    cmds = append(cmds, cmd)
  }
  return tea.Batch(cmds...)
}

func (m *ChatScreen) handleSessionEvent(event session.Event) tea.Cmd {
  switch e := event.(type) {
  case session.RefreshEvent:
    wasAtBottom := m.messages.AtBottom()
    m.refreshMessages()
    m.refreshTitle()
    m.refreshToolReview()
    m.recalculateLayout()
    if wasAtBottom {
      m.messages.GotoBottom()
    }

  case session.ErrorEvent:
    return func() tea.Msg { return m.wrap(AlertMsg{Text: e.Err.Error()}) }
  }

  return nil
}

func (m *ChatScreen) inToolReview() bool {
  return m.toolReview.Active()
}

func (m *ChatScreen) refreshToolReview() {
  pending := m.session.PendingToolCalls()
  m.toolReview.SetToolCalls(pending)
}

func (m *ChatScreen) handleKeyPress(msg tea.KeyPressMsg) tea.Cmd {
  switch {
  case key.Matches(msg, keyCycleFocus):
    if !m.inToolReview() {
      return m.cycleFocus()
    }
  case key.Matches(msg, keyCycleReasoning):
    m.cycleReasoningEffort()
    return nil
  case key.Matches(msg, keyForkChat):
    return func() tea.Msg { return m.wrap(OpenChatMsg{Chat: m.session.Chat(), Fork: true}) }
  case key.Matches(msg, keyToggleFavorite):
    return m.toggleFavorite()
  }

  if m.inToolReview() {
    return m.handleToolReviewKey(msg)
  }

  switch m.focusedComponent {
  case FocusTextarea:
    if cmd := m.input.HandleKey(msg); cmd != nil {
      return cmd
    }
  case FocusViewport:
    wrap := m.wrap
    alertFn := func(text string) tea.Cmd {
      return func() tea.Msg { return wrap(AlertMsg{Text: text}) }
    }
    if cmd := m.messages.HandleKey(msg, alertFn); cmd != nil {
      return cmd
    }
  }

  switch msg.String() {
  case "ctrl+c":
    if m.session.IsStreaming() {
      m.session.CancelStream()
      return nil
    }
    return func() tea.Msg { return CloseTabMsg{} }

  case "ctrl+j":
    if m.session.IsStreaming() {
      return nil
    }
    userInput := m.input.Value()
    if userInput != "" {
      text := m.input.Submit()
      m.messages.ResetNavigation()
      m.refreshMessages()
      m.messages.GotoBottom()
      m.recalculateLayout()

      sess := m.session
      wrap := m.wrap
      return tea.Batch(m.spinner.Tick, func() tea.Msg {
        sess.SendMessage(text)
        return wrap(sessionEventMsg{event: session.RefreshEvent{}})
      })
    }
  }

  if !m.session.IsStreaming() {
    cmd := m.input.Update(msg)
    if m.input.Height() != m.lastInputHeight {
      m.lastInputHeight = m.input.Height()
      m.recalculateLayout()
    }
    return cmd
  }
  return nil
}

func (m *ChatScreen) handleToolReviewKey(msg tea.KeyPressMsg) tea.Cmd {
  switch msg.String() {
  case "ctrl+c":
    return func() tea.Msg { return CloseTabMsg{} }

  case "ctrl+n":
    m.toolReview.NextToolCall()
    return nil

  case "ctrl+p":
    m.toolReview.PrevToolCall()
    return nil

  case "ctrl+j":
    reason := m.toolReview.InputValue()
    if reason == "" {
      m.toolReview.AcceptCurrent()
    } else {
      m.toolReview.RejectCurrent(reason)
    }
    m.toolReview.ResetInput()

    if m.toolReview.AllResolved() {
      sess := m.session
      wrap := m.wrap
      m.recalculateLayout()
      return tea.Batch(m.spinner.Tick, func() tea.Msg {
        sess.ResolveToolCalls()
        return wrap(sessionEventMsg{event: session.RefreshEvent{}})
      })
    }
    return nil
  }

  cmd := m.toolReview.UpdateInput(msg)
  return cmd
}

func (m *ChatScreen) toggleFavorite() tea.Cmd {
  sess := m.session
  wrap := m.wrap
  return func() tea.Msg {
    isFavorite := sess.ToggleFavorite()
    label := "added to"
    if !isFavorite {
      label = "removed from"
    }
    return wrap(AlertMsg{Text: fmt.Sprintf("Chat %s favorites", label)})
  }
}

func (m *ChatScreen) cycleFocus() tea.Cmd {
  switch m.focusedComponent {
  case FocusTextarea:
    m.focusedComponent = FocusViewport
    m.input.Blur()
    if m.messages.NavMessageIndex() == -1 {
      m.messages.NavigateToBottom()
    }
    m.messages.SetFocused(true)
    m.refreshMessages()
  case FocusViewport:
    m.focusedComponent = FocusTextarea
    m.messages.SetFocused(false)
    m.refreshMessages()
    return m.input.Focus()
  }
  return nil
}

func (m *ChatScreen) cycleReasoningEffort() {
  params := m.session.Params()
  switch params.ReasoningEffort {
  case aipb.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED:
    m.session.SetReasoningEffort(aipb.ReasoningEffort_REASONING_EFFORT_LOW)
  case aipb.ReasoningEffort_REASONING_EFFORT_LOW:
    m.session.SetReasoningEffort(aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM)
  case aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM:
    m.session.SetReasoningEffort(aipb.ReasoningEffort_REASONING_EFFORT_HIGH)
  case aipb.ReasoningEffort_REASONING_EFFORT_HIGH:
    m.session.SetReasoningEffort(aipb.ReasoningEffort_REASONING_EFFORT_UNSPECIFIED)
  }
  m.refreshTitle()
}

func (m *ChatScreen) refreshMessages() {
  m.messages.SetData(widget.MessagesData{
    ChatMessages:     m.session.Chat().GetMetadata().GetMessages(),
    StreamingMessage: m.session.StreamingMessage(),
    StreamError:      m.session.StreamError(),
    InjectedFiles:    m.injectedFiles,
  })
}

func (m *ChatScreen) refreshTitle() {
  m.titlebar.Refresh(m.session.Params(), m.session.TotalModelUsage(), m.session.LastModelUsage())
}

func (m *ChatScreen) recalculateLayout() {
  if m.width == 0 || m.height == 0 {
    return
  }

  m.titlebar.SetWidth(m.width)

  viewportHeight := m.height - m.titlebar.Height()
  if !m.session.IsStreaming() {
    if m.inToolReview() {
      viewportHeight -= m.toolReview.Height()
    } else {
      viewportHeight -= m.input.Height()
    }
  }
  if viewportHeight < styles.MinViewportHeight {
    viewportHeight = styles.MinViewportHeight
  }

  m.messages.SetSize(m.width, viewportHeight)
  m.input.SetWidth(m.width)
  m.toolReview.SetWidth(m.width)

  if !m.ready {
    m.ready = true
    m.refreshMessages()
    m.messages.GotoBottom()
  }
}

func (m *ChatScreen) View() string {
  if !m.ready {
    return "Initializing..."
  }

  var b strings.Builder
  b.WriteString(m.titlebar.View())
  b.WriteString("\n")
  b.WriteString(m.messages.View())

  if !m.session.IsStreaming() {
    b.WriteString("\n")
    if m.inToolReview() {
      b.WriteString(m.toolReview.View())
    } else {
      b.WriteString(m.input.View())
    }
  }

  return b.String()
}

var _ Screen = (*ChatScreen)(nil)
EOF

cat > cli/tui/screen/BUILD.plz <<'EOF'
go_library(
    name = "screen",
    srcs = [
        "chat.go",
        "screen.go",
    ],
    visibility = ["//..."],
    deps = [
        "//cli/tui/styles",
        "//cli/tui/widget",
        "//internal/session",
        "//sgpt/v1",
        "//third_party/go:charm.land__bubbles__v2__key",
        "//third_party/go:charm.land__bubbles__v2__spinner",
        "//third_party/go:charm.land__bubbles__v2__textarea",
        "//third_party/go:charm.land__bubbletea__v2",
        "//third_party/proto:malonaz__core__genproto__ai__v1",
    ],
)
EOF

############################################
# cli/tui/screen/menu — consumes the store
############################################
cat > cli/tui/screen/menu/model.go <<'EOF'
package menu

import (
  "context"
  "strings"
  "time"

  "charm.land/bubbles/v2/textarea"
  "charm.land/bubbles/v2/viewport"
  tea "charm.land/bubbletea/v2"

  "github.com/malonaz/sgpt/cli/tui/screen"
  "github.com/malonaz/sgpt/cli/tui/styles"
  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
  "github.com/malonaz/sgpt/internal/markdown"
  "github.com/malonaz/sgpt/internal/store"
)

const searchDebounceInterval = 300 * time.Millisecond

type FocusTarget int

const (
  FocusFilter FocusTarget = iota
  FocusSearch
  FocusChatList
)

type chatsLoadedMsg struct {
  Favorites     []*sgptpb.Chat
  Others        []*sgptpb.Chat
  NextPageToken string
  Err           error
  PageToken     string
  SearchQuery   string
}

type chatDeletedMsg struct {
  Name string
  Err  error
}

type chatFavoriteToggledMsg struct {
  Name      string
  Favorited bool
  Err       error
}

type searchDebounceTickMsg struct {
  Query string
}

type Model struct {
  ctx   context.Context
  store *store.Store
  wrap  screen.WrapFunc

  favorites []*sgptpb.Chat
  others    []*sgptpb.Chat

  chatCursor       int
  loading          bool
  err              error
  nextPageToken    string
  pageTokenStack   []string
  currentPageToken string

  filterInput textarea.Model
  filterText  string

  searchInput     textarea.Model
  searchQuery     string
  lastSearchQuery string

  focusTarget      FocusTarget
  selectedChatName string

  renderer       *markdown.Renderer
  listViewport   viewport.Model
  detailViewport viewport.Model
  width          int
  height         int
  ready          bool
  focused        bool
}

func New(ctx context.Context, chatStore *store.Store, wrap screen.WrapFunc) *Model {
  filterInput := textarea.New()
  filterInput.Placeholder = "Filter chats..."
  filterInput.CharLimit = 256
  filterInput.SetHeight(1)
  filterInput.ShowLineNumbers = false
  filterInput.Prompt = "/ "

  searchInput := textarea.New()
  searchInput.Placeholder = "Search chats..."
  searchInput.CharLimit = 256
  searchInput.SetHeight(1)
  searchInput.ShowLineNumbers = false
  searchInput.Prompt = "🔍 "

  renderer, _ := markdown.NewRenderer(styles.DefaultTextareaWidth)

  return &Model{
    ctx:         ctx,
    store:       chatStore,
    wrap:        wrap,
    filterInput: filterInput,
    searchInput: searchInput,
    renderer:    renderer,
    focusTarget: FocusFilter,
  }
}

func (m *Model) Init() tea.Cmd {
  return m.fetchChats("")
}

func (m *Model) visibleRowCapacity() int {
  inputHeight := 4
  headerHeight := 1
  helpBarHeight := 1
  available := m.height - 4 - inputHeight - headerHeight - helpBarHeight
  if available < 1 {
    return 1
  }
  return available
}

func (m *Model) Title() string {
  return "Menu"
}

func (m *Model) ShortTitle() string {
  return "Menu"
}

func (m *Model) SetSize(width, height int) {
  m.width = width
  m.height = height
  m.recalculateLayout()
}

func (m *Model) OnFocus() tea.Cmd {
  m.focused = true
  return m.applyFocus()
}

func (m *Model) OnBlur() {
  m.focused = false
  m.filterInput.Blur()
  m.searchInput.Blur()
}

func (m *Model) ActivateSearch() tea.Cmd {
  m.focusTarget = FocusSearch
  return m.applyFocus()
}

func (m *Model) applyFocus() tea.Cmd {
  m.filterInput.Blur()
  m.searchInput.Blur()
  switch m.focusTarget {
  case FocusFilter:
    m.filterInput.Focus()
    return textarea.Blink
  case FocusSearch:
    m.searchInput.Focus()
    return textarea.Blink
  }
  return nil
}

func (m *Model) fetchChats(pageToken string) tea.Cmd {
  m.loading = true
  wrap := m.wrap
  searchQuery := m.searchQuery
  pageSize := int32(m.visibleRowCapacity())
  return func() tea.Msg {
    ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
    defer cancel()

    if searchQuery != "" {
      chats, nextPageToken, err := m.store.SearchChats(ctx, searchQuery, pageSize, pageToken)
      if err != nil {
        return wrap(chatsLoadedMsg{Err: err, SearchQuery: searchQuery})
      }
      favorites, others := partitionByTag(chats, store.FavoriteTag)
      return wrap(chatsLoadedMsg{
        Favorites:     favorites,
        Others:        others,
        NextPageToken: nextPageToken,
        PageToken:     pageToken,
        SearchQuery:   searchQuery,
      })
    }

    favorites, err := m.store.ListFavoriteChats(ctx, pageSize)
    if err != nil {
      return wrap(chatsLoadedMsg{Err: err})
    }
    others, nextPageToken, err := m.store.ListChats(ctx, pageSize, pageToken, "")
    if err != nil {
      return wrap(chatsLoadedMsg{Err: err})
    }
    return wrap(chatsLoadedMsg{
      Favorites:     favorites,
      Others:        others,
      NextPageToken: nextPageToken,
      PageToken:     pageToken,
    })
  }
}

func (m *Model) deleteChat(name string) tea.Cmd {
  wrap := m.wrap
  return func() tea.Msg {
    err := m.store.DeleteChat(m.ctx, name)
    return wrap(chatDeletedMsg{Name: name, Err: err})
  }
}

func (m *Model) toggleFavorite(chat *sgptpb.Chat) tea.Cmd {
  wrap := m.wrap
  favorite := !store.HasTag(chat, store.FavoriteTag)
  return func() tea.Msg {
    _, err := m.store.SetFavorite(m.ctx, chat, favorite)
    return wrap(chatFavoriteToggledMsg{
      Name:      chat.GetName(),
      Favorited: favorite,
      Err:       err,
    })
  }
}

func (m *Model) resetPagination() {
  m.pageTokenStack = nil
  m.currentPageToken = ""
  m.nextPageToken = ""
}

// displayedChats returns favorites then others, with client-side filter applied.
func (m *Model) displayedChats() []*sgptpb.Chat {
  favorites := m.applyFilter(m.favorites)
  others := m.applyFilter(m.others)
  return append(favorites, others...)
}

func (m *Model) displayedFavoriteCount() int {
  return len(m.applyFilter(m.favorites))
}

func (m *Model) applyFilter(chats []*sgptpb.Chat) []*sgptpb.Chat {
  if m.filterText == "" {
    return chats
  }
  lowerFilter := strings.ToLower(m.filterText)
  var result []*sgptpb.Chat
  for _, chat := range chats {
    title := chat.GetMetadata().GetTitle()
    if strings.Contains(strings.ToLower(title), lowerFilter) || strings.Contains(strings.ToLower(chat.Name), lowerFilter) {
      result = append(result, chat)
    }
  }
  return result
}

func (m *Model) selectedChat() *sgptpb.Chat {
  displayed := m.displayedChats()
  if m.chatCursor >= 0 && m.chatCursor < len(displayed) {
    return displayed[m.chatCursor]
  }
  return nil
}

func (m *Model) updateSelection() {
  displayed := m.displayedChats()
  if m.chatCursor >= len(displayed) {
    m.chatCursor = len(displayed) - 1
  }
  if m.chatCursor < 0 {
    m.chatCursor = 0
  }
  if m.chatCursor < len(displayed) {
    m.selectedChatName = displayed[m.chatCursor].Name
  } else {
    m.selectedChatName = ""
  }
  m.detailViewport.SetContent(m.renderDetail())
  m.detailViewport.GotoTop()
}

func (m *Model) listWidth() int {
  return m.width / 2
}

func (m *Model) detailWidth() int {
  return m.width - m.listWidth() - 1
}

func (m *Model) recalculateLayout() {
  if m.width == 0 || m.height == 0 {
    return
  }

  inputHeight := 4
  totalViewportHeight := m.height - 4
  listViewportHeight := totalViewportHeight - inputHeight
  if listViewportHeight < 1 {
    listViewportHeight = 1
  }
  if totalViewportHeight < 1 {
    totalViewportHeight = 1
  }

  listWidth := m.listWidth()
  detailWidth := m.detailWidth()

  if !m.ready {
    m.listViewport = viewport.New(
      viewport.WithWidth(listWidth),
      viewport.WithHeight(listViewportHeight),
    )
    m.detailViewport = viewport.New(
      viewport.WithWidth(detailWidth),
      viewport.WithHeight(totalViewportHeight),
    )
    m.ready = true
  } else {
    m.listViewport.SetWidth(listWidth)
    m.listViewport.SetHeight(listViewportHeight)
    m.detailViewport.SetWidth(detailWidth)
    m.detailViewport.SetHeight(totalViewportHeight)
  }

  rendererWidth := detailWidth - 4
  if rendererWidth < 10 {
    rendererWidth = 10
  }
  m.renderer.SetWidth(rendererWidth)

  m.filterInput.SetWidth(listWidth - 6)
  m.searchInput.SetWidth(listWidth - 6)
}

func (m *Model) hasNextPage() bool {
  return m.nextPageToken != ""
}

func (m *Model) hasPreviousPage() bool {
  return len(m.pageTokenStack) > 0
}

func (m *Model) nextPage() tea.Cmd {
  if !m.hasNextPage() {
    return nil
  }
  m.pageTokenStack = append(m.pageTokenStack, m.currentPageToken)
  return m.fetchChats(m.nextPageToken)
}

func (m *Model) previousPage() tea.Cmd {
  if !m.hasPreviousPage() {
    return nil
  }
  previousToken := m.pageTokenStack[len(m.pageTokenStack)-1]
  m.pageTokenStack = m.pageTokenStack[:len(m.pageTokenStack)-1]
  return m.fetchChats(previousToken)
}

func (m *Model) currentPage() int {
  return len(m.pageTokenStack) + 1
}

func partitionByTag(chats []*sgptpb.Chat, tag string) (withTag []*sgptpb.Chat, withoutTag []*sgptpb.Chat) {
  for _, chat := range chats {
    if store.HasTag(chat, tag) {
      withTag = append(withTag, chat)
    } else {
      withoutTag = append(withoutTag, chat)
    }
  }
  return withTag, withoutTag
}

var _ screen.Screen = (*Model)(nil)
EOF

cat > cli/tui/screen/menu/view.go <<'EOF'
package menu

import (
  "fmt"
  "strings"
  "time"

  "charm.land/lipgloss/v2"
  aipb "github.com/malonaz/core/genproto/ai/v1"

  "github.com/malonaz/sgpt/cli/tui/styles"
  sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
  "github.com/malonaz/sgpt/internal/markdown"
  "github.com/malonaz/sgpt/internal/store"
)

func (m *Model) View() string {
  if !m.ready {
    return "Loading..."
  }

  var b strings.Builder

  modeLabel := "List"
  if m.searchQuery != "" {
    modeLabel = "Search"
  }
  header := styles.TitleStyle.Width(m.width).Render(fmt.Sprintf(" 📋 Chat History (%s, page %d) ", modeLabel, m.currentPage()))
  b.WriteString(header)
  b.WriteString("\n")

  var leftPanel strings.Builder
  filterStyle := m.inputStyle(FocusFilter)
  leftPanel.WriteString(filterStyle.Width(m.listWidth() - 2).Render(m.filterInput.View()))
  leftPanel.WriteString("\n")
  searchStyle := m.inputStyle(FocusSearch)
  leftPanel.WriteString(searchStyle.Width(m.listWidth() - 2).Render(m.searchInput.View()))
  leftPanel.WriteString("\n")
  leftPanel.WriteString(m.listViewport.View())

  detailPanel := m.detailViewport.View()
  separator := lipgloss.NewStyle().Foreground(styles.BorderColor).Render(
    strings.Repeat("│\n", m.height-3),
  )

  joined := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel.String(), separator, detailPanel)
  b.WriteString(joined)

  b.WriteString("\n")
  var pagination strings.Builder
  if m.hasPreviousPage() {
    pagination.WriteString("◀ [ ")
  }
  pagination.WriteString(fmt.Sprintf("page %d", m.currentPage()))
  if m.hasNextPage() {
    pagination.WriteString(" ] ▶")
  }
  helpText := fmt.Sprintf("C-p/C-n: navigate │ Enter: open │ Alt+d: delete │ Alt+f: favorite │ Alt+r: refresh │ %s", pagination.String())
  b.WriteString(styles.HelpStyle.Render(helpText))

  return b.String()
}

func (m *Model) inputStyle(target FocusTarget) lipgloss.Style {
  if m.focusTarget == target {
    return styles.SearchInputStyle.BorderForeground(styles.PrimaryColor)
  }
  return styles.SearchInputStyle.BorderForeground(styles.BorderColor)
}

func (m *Model) renderList() string {
  if m.loading {
    return styles.DimTextStyle.Render("Loading chats...")
  }
  if m.err != nil {
    return styles.ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err))
  }

  displayed := m.displayedChats()
  if len(displayed) == 0 {
    if m.searchQuery != "" {
      return styles.DimTextStyle.Render("No search results")
    }
    if m.filterText != "" {
      return styles.DimTextStyle.Render("No chats match filter")
    }
    return styles.DimTextStyle.Render("No chats yet")
  }

  listWidth := m.listWidth()
  favCount := m.displayedFavoriteCount()

  var b strings.Builder

  if favCount > 0 {
    sectionHeader := styles.MenuHeaderStyle.Width(listWidth).Render("⭐ Favorites")
    b.WriteString(sectionHeader)
    b.WriteString("\n")
    b.WriteString(m.renderChatRows(displayed[:favCount], listWidth, 0))
    if favCount < len(displayed) {
      b.WriteString("\n\n")
    }
  }

  if favCount < len(displayed) {
    sectionHeader := styles.MenuHeaderStyle.Width(listWidth).Render("📋 Chats")
    b.WriteString(sectionHeader)
    b.WriteString("\n")
    b.WriteString(m.renderChatRows(displayed[favCount:], listWidth, favCount))
  }

  return b.String()
}

func (m *Model) renderChatRows(chats []*sgptpb.Chat, listWidth int, globalIndexOffset int) string {
  var b strings.Builder
  for i, chat := range chats {
    title := chat.GetMetadata().GetTitle()
    title = styles.Truncate(title, 28)

    messageCount := len(chat.GetMetadata().GetMessages())
    updated := relativeTime(chat.GetUpdateTime().AsTime())

    tags := strings.Join(chat.GetTags(), ",")
    tags = styles.Truncate(tags, 15)

    line := fmt.Sprintf("%-30s %-5d %-10s", title, messageCount, updated)
    coloredTags := styles.MenuTagStyle.Render(tags)

    globalIndex := globalIndexOffset + i
    style := styles.MenuItemStyle
    if m.focusTarget == FocusChatList && globalIndex == m.chatCursor {
      style = styles.MenuSelectedStyle
    }
    b.WriteString(style.Width(listWidth).Render(line + coloredTags))
    if i < len(chats)-1 {
      b.WriteString("\n")
    }
  }
  return b.String()
}

func (m *Model) renderDetail() string {
  detailWidth := m.detailWidth()

  chat := m.selectedChat()
  if chat == nil {
    return styles.DimTextStyle.Render(" Select a chat to preview")
  }

  messages := chat.GetMetadata().GetMessages()
  if len(messages) == 0 {
    return styles.DimTextStyle.Render(" No messages in this chat")
  }

  var b strings.Builder
  title := chat.GetName()
  if store.HasTag(chat, store.FavoriteTag) {
    title = "⭐ " + title
  }
  b.WriteString(styles.MenuTitleStyle.Render(fmt.Sprintf(" %s", styles.Truncate(title, detailWidth-2))))
  b.WriteString("\n")
  model := chat.GetMetadata().GetCurrentModel()
  if model != "" {
    b.WriteString(styles.DimTextStyle.Render(fmt.Sprintf(" Model: %s", model)))
    b.WriteString("\n")
  }
  if tags := chat.GetTags(); len(tags) > 0 {
    b.WriteString(styles.MenuTagStyle.Render(fmt.Sprintf(" Tags: %s", strings.Join(tags, ", "))))
    b.WriteString("\n")
  }
  b.WriteString(styles.DividerStyle.Render(strings.Repeat("─", detailWidth)))
  b.WriteString("\n")

  contentWidth := detailWidth - 4

  for i, chatMessage := range messages {
    if i > 0 {
      b.WriteString("\n")
    }
    message := chatMessage.GetMessage()
    switch message.GetRole() {
    case aipb.Role_ROLE_USER:
      b.WriteString(styles.UserLabelStyle.Render(" You:"))
      b.WriteString("\n")
      for _, block := range message.GetBlocks() {
        text := block.GetText()
        if text != "" {
          blocks := markdown.ParseBlocks(text)
          rendered := m.renderer.ToMarkdown(-1, false, blocks...)
          b.WriteString("  " + strings.ReplaceAll(rendered, "\n", "\n  "))
          b.WriteString("\n")
        }
      }

    case aipb.Role_ROLE_ASSISTANT:
      b.WriteString(styles.AILabelStyle.Render(" Assistant:"))
      b.WriteString("\n")
      for _, block := range message.GetBlocks() {
        if thought := block.GetThought(); thought != "" {
          blocks := markdown.ParseBlocks(thought)
          rendered := m.renderer.ToMarkdown(-1, false, blocks...)
          b.WriteString(styles.ThoughtStyle.Render("  " + strings.ReplaceAll(rendered, "\n", "\n  ")))
          b.WriteString("\n")
        }
        if text := block.GetText(); text != "" {
          blocks := markdown.ParseBlocks(text)
          rendered := m.renderer.ToMarkdown(-1, false, blocks...)
          b.WriteString("  " + strings.ReplaceAll(rendered, "\n", "\n  "))
          b.WriteString("\n")
        }
        if toolCall := block.GetToolCall(); toolCall != nil {
          b.WriteString(styles.ToolLabelStyle.Render(fmt.Sprintf("  🔧 %s", toolCall.Name)))
          b.WriteString("\n")
        }
      }

    case aipb.Role_ROLE_TOOL:
      b.WriteString(styles.ToolLabelStyle.Render(" ⚡ Tool Result"))
      b.WriteString("\n")
      for _, block := range message.GetBlocks() {
        if toolResult := block.GetToolResult(); toolResult != nil {
          var content string
          if toolResult.GetError() != nil {
            content = fmt.Sprintf("Error: %s", toolResult.GetError().GetMessage())
          } else if structured := toolResult.GetStructuredContent(); structured != nil {
            bytes, _ := structured.MarshalJSON()
            content = fmt.Sprintf("```json\n%s\n```", string(bytes))
          } else {
            content = toolResult.GetContent()
          }
          if content != "" {
            truncated := styles.Truncate(content, contentWidth*2)
            b.WriteString(styles.DimTextStyle.Render("  " + strings.ReplaceAll(truncated, "\n", "\n  ")))
            b.WriteString("\n")
          }
        }
      }

    case aipb.Role_ROLE_SYSTEM:
      b.WriteString(styles.SystemStyle.Render(fmt.Sprintf(" System: %s", styles.Truncate(blockText(message), 60))))
      b.WriteString("\n")
    }
  }

  return b.String()
}

func blockText(message *aipb.Message) string {
  for _, block := range message.GetBlocks() {
    if text := block.GetText(); text != "" {
      return text
    }
  }
  return ""
}

func relativeTime(t time.Time) string {
  d := time.Since(t)
  switch {
  case d < time.Minute:
    return fmt.Sprintf("%ds ago", int(d.Seconds()))
  case d < time.Hour:
    return fmt.Sprintf("%dm ago", int(d.Minutes()))
  case d < 24*time.Hour:
    return fmt.Sprintf("%dh ago", int(d.Hours()))
  case d < 30*24*time.Hour:
    return fmt.Sprintf("%dd ago", int(d.Hours()/24))
  case d < 365*24*time.Hour:
    return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
  default:
    return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
  }
}
EOF

cat > cli/tui/screen/menu/BUILD.plz <<'EOF'
go_library(
    name = "menu",
    srcs = [
        "model.go",
        "update.go",
        "view.go",
    ],
    visibility = ["//..."],
    deps = [
        "//cli/tui/screen",
        "//cli/tui/styles",
        "//internal/markdown",
        "//internal/store",
        "//sgpt/v1",
        "//third_party/go:charm.land__bubbles__v2__key",
        "//third_party/go:charm.land__bubbles__v2__textarea",
        "//third_party/go:charm.land__bubbles__v2__viewport",
        "//third_party/go:charm.land__bubbletea__v2",
        "//third_party/go:charm.land__lipgloss__v2",
        "//third_party/proto:malonaz__core__genproto__ai__v1",
    ],
)
EOF

############################################
# cli/tui/widget — titlebar uses session.Params; drop dead code
############################################
cat > cli/tui/widget/titlebar.go <<'EOF'
package widget

import (
  "fmt"
  "strings"

  "charm.land/lipgloss/v2"
  aipb "github.com/malonaz/core/genproto/ai/v1"

  "github.com/malonaz/sgpt/cli/tui/styles"
  "github.com/malonaz/sgpt/internal/session"
)

type TitleBar struct {
  width  int
  title  string
  height int
}

func NewTitleBar() *TitleBar {
  return &TitleBar{}
}

func (t *TitleBar) SetWidth(width int) {
  t.width = width
}

func (t *TitleBar) Height() int {
  return t.height
}

// Refresh rebuilds the title string from session state.
func (t *TitleBar) Refresh(params session.Params, totalUsage, lastUsage *aipb.ModelUsage) {
  roleName := "anon"
  if params.Role != nil {
    roleName = params.Role.Name
  }

  reasoningStr := "none"
  switch params.ReasoningEffort {
  case aipb.ReasoningEffort_REASONING_EFFORT_LOW:
    reasoningStr = "low"
  case aipb.ReasoningEffort_REASONING_EFFORT_MEDIUM:
    reasoningStr = "medium"
  case aipb.ReasoningEffort_REASONING_EFFORT_HIGH:
    reasoningStr = "high"
  }

  toolsStr := strings.Join(params.Tools, " + ")
  if toolsStr != "" {
    toolsStr = " | 🔧 " + toolsStr
  }

  totalInputTokens := totalUsage.GetInputToken().GetQuantity() + totalUsage.GetInputTokenCacheRead().GetQuantity()
  totalOutputTokens := totalUsage.GetOutputToken().GetQuantity() + totalUsage.GetOutputReasoningToken().GetQuantity()
  totalPrice := totalUsage.GetInputToken().GetPrice() +
    totalUsage.GetOutputToken().GetPrice() +
    totalUsage.GetOutputReasoningToken().GetPrice() +
    totalUsage.GetInputTokenCacheRead().GetPrice() +
    totalUsage.GetInputTokenCacheWrite().GetPrice()

  tokenStr := fmt.Sprintf("↑%s ↓%s $%.4f", formatTokenCount(totalInputTokens), formatTokenCount(totalOutputTokens), totalPrice)

  contextStr := ""
  if contextLimit := params.Model.GetTtt().GetContextTokenLimit(); contextLimit > 0 {
    lastInputTokens := lastUsage.GetInputToken().GetQuantity() + lastUsage.GetInputTokenCacheRead().GetQuantity()
    usagePercent := float64(lastInputTokens) / float64(contextLimit) * 100
    contextStr = fmt.Sprintf(" │ 📦 %.0f%% (%s/%s)", usagePercent, formatTokenCount(lastInputTokens), formatTokenCount(contextLimit))
  }

  modelResourceName := &aipb.ModelResourceName{}
  modelResourceName.UnmarshalString(params.Model.Name)
  modelStr := fmt.Sprintf("%s/%s", modelResourceName.Provider, modelResourceName.Model)

  t.title = fmt.Sprintf(
    " 🤖 %s │ 👤 %s │ 🧠 %s │ 📊 %s%s%s ",
    modelStr, roleName, reasoningStr, tokenStr, contextStr, toolsStr,
  )
}

func (t *TitleBar) View() string {
  rendered := styles.TitleStyle.Width(t.width).Render(t.title)
  t.height = lipgloss.Height(rendered)
  return rendered
}

func formatTokenCount(count int32) string {
  if count < 1000 {
    return fmt.Sprintf("%d", count)
  }
  if count < 1000000 {
    return fmt.Sprintf("%.1fk", float64(count)/1000)
  }
  return fmt.Sprintf("%.1fm", float64(count)/1000000)
}
EOF

# Remove the unused truncateLines helper from messages.go.
perl -0777 -pi -e 's/\nfunc truncateLines\(s string, maxLines int\) string \{[^}]*\}\n//' cli/tui/widget/messages.go

cat > cli/tui/widget/BUILD.plz <<'EOF'
go_library(
    name = "widget",
    srcs = [
        "editor.go",
        "input.go",
        "messages.go",
        "tab_bar.go",
        "titlebar.go",
        "tool_review.go",
    ],
    visibility = ["//..."],
    deps = [
        "//cli/tui/styles",
        "//internal/history",
        "//internal/markdown",
        "//internal/session",
        "//internal/tools",
        "//sgpt/v1",
        "//third_party/go:charm.land__bubbles__v2__key",
        "//third_party/go:charm.land__bubbles__v2__textarea",
        "//third_party/go:charm.land__bubbles__v2__viewport",
        "//third_party/go:charm.land__bubbletea__v2",
        "//third_party/go:charm.land__lipgloss__v2",
        "//third_party/go:github.com__malonaz__core__go__ai",
        "//third_party/go:github.com__malonaz__core__go__pbutil",
        "//third_party/go:golang.design__x__clipboard",
        "//third_party/proto:malonaz__core__genproto__ai__v1",
    ],
)
EOF

############################################
# Format & summary
############################################
command -v gofmt >/dev/null 2>&1 && gofmt -w cli internal || true

echo "Refactor applied:"
echo "  + internal/store    (single RPC boundary: chats, models, streaming)"
echo "  + internal/session  (moved from cli/cli_service/session; persists via store)"
echo "  ~ internal/tools    (unified Tool interface + Registry; handler.go removed)"
echo "  ~ internal/toolengine (implements tools.Tool: Review/Execute)"
echo "  ~ cli/tui           (no direct RPCs; store injected into app & menu)"
echo "  - cli/cli_service   (deleted)"
echo "  - cli/chat/cache.go (moved into store)"
echo "Next: plz build //... && plz test //..."
