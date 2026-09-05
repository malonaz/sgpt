// Command screenshots renders real TUI frames from scripted conversations
// against an in-process AiService, writing one ANSI text file per scene.
// Pipe the output through `freeze` for SVG/PNG.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	aiservicepb "github.com/malonaz/core/genproto/ai/ai_service/v1"
	aipb "github.com/malonaz/core/genproto/ai/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/malonaz/sgpt/cli/tui"
	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/session"
	"github.com/malonaz/sgpt/internal/store"
	"github.com/malonaz/sgpt/internal/tool"
	"github.com/malonaz/sgpt/internal/tool/agent"
	"github.com/malonaz/sgpt/internal/tool/diff"
	toolio "github.com/malonaz/sgpt/internal/tool/io"
	"github.com/malonaz/sgpt/internal/tool/shell"
)

const (
	width  = 120
	height = 36
	model  = "providers/anthropic/models/claude-opus-5"
)

// scene is one screenshot: scripted model responses plus the user actions
// that drive the UI into the frame we want.
type scene struct {
	name      string
	title     string
	responses [][]*aipb.Block
	drive     func(r *runtime, s *session.Session)
}

func main() {
	outDir := flag.String("out", "frames", "directory for ANSI frames")
	workDir := flag.String("workdir", "", "directory with fixture files (default: temp copy of ./fixtures)")
	flag.Parse()
	if err := run(*outDir, *workDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(outDir, workDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if workDir == "" {
		var err error
		if workDir, err = copyFixtures(); err != nil {
			return err
		}
		defer os.RemoveAll(workDir)
	}
	// Tools resolve paths against the cwd; edits land in the throwaway copy.
	if err := os.Chdir(workDir); err != nil {
		return err
	}
	for _, sc := range scenes() {
		frame, err := render(sc)
		if err != nil {
			return fmt.Errorf("%s: %w", sc.name, err)
		}
		path := filepath.Join(outDir, sc.name+".ansi")
		if err := os.WriteFile(path, []byte(frame), 0o644); err != nil {
			return err
		}
		fmt.Println(path)
	}
	return nil
}

func render(sc scene) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, closeServer, err := startServer(&fakeAiService{responses: sc.responses, title: sc.title})
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
	registry.Register(tool.HandlerIDDiff, &diff.Tool{})
	registry.Register(tool.HandlerIDReplace, &toolio.ReplaceTool{})
	registry.Register(tool.HandlerIDAgent, &agent.Tool{})
	for _, name := range tool.BuiltinNames() {
		builtin, _ := tool.Builtin(name)
		registry.AddTools(builtin)
	}
	toolNames := []string{"read_files", "exec_shell", "diff"}

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

// copyFixtures clones ./fixtures next to this source into a temp dir so edit
// tools can mutate files freely.
func copyFixtures() (string, error) {
	source := "fixtures"
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
