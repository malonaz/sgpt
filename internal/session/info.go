package session

import (
	"sort"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	"github.com/malonaz/sgpt/internal/store"
)

// Info is a point-in-time snapshot of everything worth knowing about a chat:
// message counts, token/cost breakdown and what currently occupies the
// context. Computed on demand (the info modal), never cached — the session's
// own fields stay the single source of truth.
type Info struct {
	ChatName  string
	Title     string
	Model     string
	Provider  string
	Role      string
	Reasoning aipb.ReasoningEffort
	Favorite  bool

	// Per-role tallies are the chat's server-maintained live counts;
	// ContextMessages and ToolCalls are derived from the local history.
	UserMessages      int
	AssistantMessages int
	ToolMessages      int
	SystemMessages    int
	ContextMessages   int
	TotalMessages     int
	QueuedMessages    int
	ToolCalls         int
	// ToolNameToCalls counts calls per tool name, for the busiest-tools list.
	ToolNameToCalls map[string]int

	// Usage is cumulative across the chat's generations; ContextUsage is the
	// last turn's, which is what actually fills the context window.
	Usage        *aipb.ModelUsage
	ContextUsage *aipb.ModelUsage
	ContextLimit int32
	OutputLimit  int32
	Price        float64

	// Lores and Files partition the injected context by origin: a lore is an
	// injected file that resolves to a lore library.
	Lores []string
	Files []string

	// EnabledTools is the user-facing selection advertised to the model;
	// AvailableTools is everything selectable. AutoAcceptedTools skip review.
	EnabledTools       []string
	AvailableTools     []string
	AutoAcceptedTools  []string
	SupportsToolCall   bool
	SupportsReasoning  bool
	ProviderCacheReads bool
}

// Info computes the snapshot. Blocking only on the session mutex.
func (s *Session) Info() *Info {
	// TotalModelUsage() and LastModelUsage() take the lock themselves.
	usage, contextUsage := s.TotalModelUsage(), s.LastModelUsage()

	s.mu.Lock()
	defer s.mu.Unlock()

	info := &Info{
		ChatName:          s.chat.GetName(),
		Title:             s.chat.GetTitle(),
		Model:             s.params.Model.GetName(),
		Reasoning:         s.params.ReasoningEffort,
		Favorite:          store.IsFavorite(s.chat),
		ToolNameToCalls:   map[string]int{},
		Usage:             usage,
		ContextUsage:      contextUsage,
		ContextLimit:      s.params.Model.GetTtt().GetContextTokenLimit(),
		OutputLimit:       s.params.Model.GetTtt().GetOutputTokenLimit(),
		Price:             s.chat.GetPrice(),
		QueuedMessages:    len(s.queuedMessages),
		UserMessages:      int(s.chat.GetUserMessageCount()),
		AssistantMessages: int(s.chat.GetAssistantMessageCount()),
		ToolMessages:      int(s.chat.GetToolMessageCount()),
		SystemMessages:    int(s.chat.GetSystemMessageCount()),
		AvailableTools:    append([]string(nil), s.params.AvailableToolNames...),
	}
	if s.params.Role != nil {
		info.Role = s.params.Role.Name
	}
	if s.params.Model.GetTtt() != nil {
		info.SupportsToolCall = s.params.Model.GetTtt().GetToolCall()
		info.SupportsReasoning = s.params.Model.GetTtt().GetReasoning()
	}
	modelResourceName := &aipb.ModelRn{}
	if err := modelResourceName.UnmarshalString(s.params.Model.GetName()); err == nil {
		info.Provider, info.Model = modelResourceName.Provider, modelResourceName.Model
	}

	info.TotalMessages = info.UserMessages + info.AssistantMessages + info.ToolMessages + info.SystemMessages

	for _, message := range s.messages {
		// Soft-deleted messages are gone from the model's view of the chat;
		// counting them would misreport what the context actually holds.
		if message.GetDeleteTime() != nil {
			continue
		}
		if store.IsContextMessage(message) {
			info.ContextMessages++
		}
		for _, block := range message.GetBlocks() {
			if toolCall := block.GetToolCall(); toolCall != nil {
				info.ToolCalls++
				info.ToolNameToCalls[toolCall.GetName()]++
			}
		}
	}

	// Injected files split by origin: lore selectors are shown by canonical
	// name, everything else by path.
	for _, path := range s.injectedFilePaths {
		if s.params.LoreNameForPath != nil {
			if name, ok := s.params.LoreNameForPath(path); ok {
				info.Lores = append(info.Lores, name)
				continue
			}
		}
		info.Files = append(info.Files, path)
	}

	for name := range s.enabledUserToolNameSet {
		info.EnabledTools = append(info.EnabledTools, name)
	}
	for name := range s.autoAcceptedToolNameSet {
		info.AutoAcceptedTools = append(info.AutoAcceptedTools, name)
	}
	sort.Strings(info.EnabledTools)
	sort.Strings(info.AutoAcceptedTools)
	return info
}
