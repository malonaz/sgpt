// Command screenshots renders real TUI frames from scripted conversations
// against an in-process AiService, writing one ANSI text file per scene.
// Pipe the output through `freeze` for SVG/PNG.
package main

import (
	"context"
	"flag"
	"fmt"
	"image/color"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/malonaz/sgpt/cli/tui"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/lore"
	"github.com/malonaz/sgpt/internal/repo"
	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/agent"
	"github.com/malonaz/sgpt/internal/tool/diff"
	toolio "github.com/malonaz/sgpt/internal/tool/io"
	"github.com/malonaz/sgpt/internal/tool/lores"
	"github.com/malonaz/sgpt/internal/tool/shell"
)

const model = "providers/anthropic/models/claude-opus-5"

// scene is one screenshot: scripted model responses plus the user actions
// that drive the UI into the frame we want.
type scene struct {
	name      string
	title     string
	responses [][]*aipb.Block
	// chats and messages seed the server's history (the Chats tab).
	chats    []*aipb.Chat
	messages map[string][]*aipb.Message
	drive    func(r *runtime, s *session.Session)
}

// registered holds every scene; each scenes_*.go file registers its own from
// init, so areas of the UI add scenes without touching each other.
var registered []scene

func register(scenes ...scene) { registered = append(registered, scenes...) }

func main() {
	outDir := flag.String("out", "frames", "directory for ANSI frames")
	fixtures := flag.String("fixtures", "fixtures", "directory of fixture files; each scene works on a fresh copy")
	only := flag.String("scene", "", "render only the scenes whose name contains this")
	width := flag.Int("width", 120, "terminal width")
	height := flag.Int("height", 36, "terminal height")
	text := flag.Bool("text", false, "also write a plain-text .txt (ANSI stripped) per scene")
	background := flag.String("background", "#282a36", "terminal background the TUI is told about (surfaces derive from it)")
	flag.Parse()
	if err := run(*outDir, *fixtures, *only, *width, *height, *text, lipgloss.Color(*background)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(outDir, fixtures, only string, width, height int, text bool, background color.Color) error {
	// Absolute: each scene chdirs into its own fixture copy.
	outDir, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	if fixtures, err = filepath.Abs(fixtures); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, sc := range registered {
		if !strings.Contains(sc.name, only) {
			continue
		}
		frame, err := renderInCopy(fixtures, sc, width, height, background)
		if err != nil {
			return fmt.Errorf("%s: %w", sc.name, err)
		}
		path := filepath.Join(outDir, sc.name+".ansi")
		if err := os.WriteFile(path, []byte(frame), 0o644); err != nil {
			return err
		}
		fmt.Println(path)
		if text {
			if err := os.WriteFile(strings.TrimSuffix(path, ".ansi")+".txt", []byte(ansi.Strip(frame)), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderInCopy renders a scene inside a fresh copy of the fixtures: tools
// resolve paths against the cwd, and one scene's edits must not leak into the
// next.
func renderInCopy(fixtures string, sc scene, width, height int, background color.Color) (string, error) {
	workDir, err := copyFixtures(fixtures)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(workDir)
	if err := os.Chdir(workDir); err != nil {
		return "", err
	}
	return render(sc, width, height, background)
}

func render(sc scene, width, height int, background color.Color) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, closeServer, err := startServer(&fakeAiService{responses: sc.responses, title: sc.title, chats: sc.chats, messages: sc.messages})
	if err != nil {
		return "", err
	}
	defer closeServer()

	configuration := &sgptpb.Configuration{
		Chat: &sgptpb.ChatConfiguration{User: "organizations/acme/users/ada"},
	}
	chatStore := store.New(configuration, client)

	registry := tool.NewRegistry()
	registry.Register(tool.HandlerIDShell, &shell.Tool{})
	registry.Register(tool.HandlerIDReadFiles, &toolio.ReadFilesTool{})
	registry.Register(tool.HandlerIDReadImage, &toolio.ReadImageTool{})
	registry.Register(tool.HandlerIDReadDocument, &toolio.ReadDocumentTool{})
	registry.Register(tool.HandlerIDDiff, &diff.Tool{})
	registry.Register(tool.HandlerIDReplace, &toolio.ReplaceTool{})
	// The fixtures are the repo: search_lores searches their (empty) library.
	registry.Register(tool.HandlerIDSearchLores, &lores.Tool{Index: lore.NewIndex(".", &repo.Imports{})})
	for handlerID, agentTool := range agent.NewManager(nil, nil).Tools() {
		registry.Register(handlerID, agentTool)
	}
	for _, name := range tool.BuiltinNames() {
		builtinTools, _ := tool.Builtin(name)
		registry.AddTools(builtinTools...)
	}
	toolNames := []string{"read_files", "exec_shell", "diff", "replace", "search_lores", "create_agents"}

	chat := &aipb.Chat{}
	store.SetCurrentModel(chat, model)
	params := session.Params{
		Model:              &aipb.Model{Name: model},
		Role:               &sgptpb.Role{Name: "//:default", Alias: "default"},
		Tools:              toolNames,
		AvailableToolNames: tool.BuiltinNames(),
		ResolveTool:        func(_ context.Context, name string) ([]string, error) { return []string{name}, nil },
		SystemPrompt:       "You are SGPT.",
		LoreNameForPath:    func(string) (string, bool) { return "", false },
	}
	chatSession := session.New(ctx, chatStore, registry, chat, nil, params)
	app := tui.NewApp(ctx, chatStore, registry, chatSession, params)

	r := newRuntime(app)
	r.start()
	r.send(tea.WindowSizeMsg{Width: width, Height: height})
	// The answer to the app's background request, as a terminal would send.
	r.send(tea.BackgroundColorMsg{Color: background})
	r.settle(200 * time.Millisecond)
	sc.drive(r, chatSession)
	frame := r.view()
	r.stop()
	return frame, nil
}

func startServer(service aiservicepb.AiServiceServer) (aiservicepb.AiServiceClient, func(), error) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	aiservicepb.RegisterAiServiceServer(server, service)
	go server.Serve(listener) //nolint:errcheck

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		return nil, nil, err
	}
	return aiservicepb.NewAiServiceClient(conn), func() { conn.Close(); server.Stop() }, nil
}

// copyFixtures clones source into a temp dir so edit tools can mutate files
// freely.
func copyFixtures(source string) (string, error) {
	target, err := os.MkdirTemp("", "sgpt-screenshots-")
	if err != nil {
		return "", err
	}
	err = filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(source, path)
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o644)
	})
	return target, err
}
