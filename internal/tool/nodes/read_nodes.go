// Package nodes implements the read_nodes tool: the mechanism by which a
// chat reads knowledge-graph nodes beyond the ones injected at launch.
package nodes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	aipb "github.com/malonaz/core/genproto/ai/v1"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/graph"
	"github.com/malonaz/sgpt/internal/tool"
)

// ReadNodes is the tool definition, built from ToolService.ReadNodes.
var ReadNodes = tool.MustBuildTool("read_nodes", tool.HandlerIDReadNodes, "sgpt.v1.ToolService.ReadNodes")

func parseReadNodesArguments(toolCall *aipb.ToolCall) (*sgptpb.ReadNodesRequest, error) {
	readNodesRequest := &sgptpb.ReadNodesRequest{}
	if err := tool.UnmarshalArguments(toolCall, readNodesRequest); err != nil {
		return nil, err
	}
	if len(readNodesRequest.GetNames()) == 0 {
		return nil, fmt.Errorf("no names specified")
	}
	return readNodesRequest, nil
}

// Tool reads knowledge-graph nodes of the enclosing graph.
type Tool struct {
	// IgnorePatterns are the configuration's extra ignore patterns, unioned
	// with the graph's own at scan time.
	IgnorePatterns []string
	// Imports are the configuration's external repos, addressable via
	// "@{name}//{selector}" node names.
	Imports []*sgptpb.Import
}

func (t *Tool) Review(_ context.Context, toolCall *aipb.ToolCall) (*sgptpb.ToolCallMetadata, error) {
	if _, err := parseReadNodesArguments(toolCall); err != nil {
		return nil, err
	}
	// Auto-execution is declared on the proto method (NO_SIDE_EFFECTS).
	return &sgptpb.ToolCallMetadata{
		DisplayMessage: &sgptpb.DisplayMessage{},
		AutoExecute:    tool.NoSideEffects(toolCall),
	}, nil
}

func (t *Tool) Execute(_ context.Context, toolCall *aipb.ToolCall) (*aipb.ToolResult, error) {
	readNodesRequest, err := parseReadNodesArguments(toolCall)
	if err != nil {
		return nil, err
	}
	root, err := graph.FindRoot(".")
	if err != nil {
		return nil, err
	}
	primary, err := graph.Scan(root, t.IgnorePatterns)
	if err != nil {
		return nil, err
	}
	forest := graph.NewForest(primary, t.Imports, configuration.LoadIgnore)

	nodes := make([]*sgptpb.ReadNodesResponse_Node, 0, len(readNodesRequest.GetNames()))
	for _, name := range readNodesRequest.GetNames() {
		result := &sgptpb.ReadNodesResponse_Node{Name: name}
		tree, nodeFile, err := forest.Resolve(name)
		if err != nil {
			// Per-node errors are part of the result so one bad name doesn't
			// sink the whole read.
			result.Error = err.Error()
			nodes = append(nodes, result)
			continue
		}
		dir := tree.PathToDir[nodeFile.Dir]
		result.Content = nodeFile.Message.GetContent()
		result.Parents = related(tree, tree.Parents(dir))
		result.Children = related(tree, tree.Children(dir))
		result.Files = pulledFiles(tree, nodeFile)
		nodes = append(nodes, result)
	}
	readNodesResponse := &sgptpb.ReadNodesResponse{Nodes: nodes}
	return tool.NewStructuredToolResult(toolCall, readNodesResponse)
}

func related(tree *graph.Tree, nodeFiles []*graph.NodeFile) []*sgptpb.ReadNodesResponse_Related {
	relatedNodes := make([]*sgptpb.ReadNodesResponse_Related, 0, len(nodeFiles))
	for _, nodeFile := range nodeFiles {
		relatedNodes = append(relatedNodes, &sgptpb.ReadNodesResponse_Related{
			// Labels are fully qualified so an imported graph's nodes stay
			// addressable in follow-up reads.
			Name:    tree.Label(nodeFile),
			Summary: nodeFile.Message.GetSummary(),
		})
	}
	return relatedNodes
}

// pulledFiles reads the files a node pulls in verbatim; per-file errors are
// part of the result so a vanished file doesn't sink the read.
func pulledFiles(tree *graph.Tree, nodeFile *graph.NodeFile) []*sgptpb.ReadFilesResponse_File {
	files := make([]*sgptpb.ReadFilesResponse_File, 0, len(nodeFile.Message.GetFiles()))
	for _, path := range nodeFile.Message.GetFiles() {
		file := &sgptpb.ReadFilesResponse_File{Path: path}
		content, err := os.ReadFile(filepath.Join(tree.Root, path))
		if err != nil {
			file.Error = err.Error()
		} else {
			file.Content = string(content)
		}
		files = append(files, file)
	}
	return files
}

// RenderHeader shows the node names being read instead of the tool name.
func (t *Tool) RenderHeader(toolCall *aipb.ToolCall) (string, bool) {
	readNodesRequest, err := parseReadNodesArguments(toolCall)
	if err != nil {
		return "", false
	}
	names := make([]string, 0, len(readNodesRequest.GetNames()))
	for _, name := range readNodesRequest.GetNames() {
		names = append(names, fmt.Sprintf("`%s`", name))
	}
	if len(names) > 3 {
		names = append(names[:3], fmt.Sprintf("+%d more", len(names)-3))
	}
	return "🧭 " + strings.Join(names, ", "), true
}

var (
	_ tool.Tool           = (*Tool)(nil)
	_ tool.HeaderRenderer = (*Tool)(nil)
)

func init() { tool.RegisterBuiltin(ReadNodes) }
