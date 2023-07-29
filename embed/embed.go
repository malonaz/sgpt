package embed

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	"github.com/malonaz/sgpt/embed/store"
	"github.com/malonaz/sgpt/internal/cli"
	"github.com/malonaz/sgpt/internal/configuration"
	"github.com/malonaz/sgpt/internal/file"
	"github.com/malonaz/sgpt/internal/model"
)

// NewCmd instantiates and returns the diff command.
func NewCmd(openAIClient *openai.Client, config *configuration.Config) *cobra.Command {
	// Initialize embed directory.
	err := file.CreateDirectoryIfNotExist(config.EmbedDirectory)
	cobra.CheckErr(err)

	var opts struct {
		Force bool
	}
	cmd := &cobra.Command{
		Use:   "embed",
		Short: "Generate embeddings for a repo",
		Long:  "Generate embeddings for a repo",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			// Check that we are in a git repo.
			ok, err := file.DirectoryExists(".git")
			cobra.CheckErr(err)
			if !ok {
				cli.FileInfo("Error: must be in root of git repo\n")
				return
			}
			// Set the model.
			optsModel := &model.Opts{Model: "text-embedding-ada-002"}
			model, err := model.Parse(optsModel, config)
			cobra.CheckErr(err)

			s, err := LoadStore(config)
			cobra.CheckErr(err)

			// Headers.
			cli.Separator()
			cli.Title("SGPT EMBED [%s]", model.ID)
			cli.Separator()

			// Run git diff.
			path, err := exec.LookPath("git")
			cobra.CheckErr(errors.Wrapf(err, "git not found in your PATH"))
			gitListFilesCommand := exec.Command(path, "ls-files")
			bytesBuffer := &bytes.Buffer{}
			gitListFilesCommand.Stdout = bytesBuffer
			err = gitListFilesCommand.Run()
			cobra.CheckErr(err)
			gitFiles := strings.Split(bytesBuffer.String(), "\n")
			gitFiles = gitFiles[:len(gitFiles)-1]

			// Filter out files we don't want.
			filteredGitFiles := []string{}
			for _, gitFile := range gitFiles {
				ignore := false
				for _, ignoreFile := range config.DiffIgnoreFiles {
					if strings.Contains(gitFile, ignoreFile) {
						cli.FileInfo("Ignoring %s\n", gitFile)
						ignore = true
						break
					}
				}
				if !ignore {
					filteredGitFiles = append(filteredGitFiles, gitFile)
				}
			}
			cli.Separator()

			// Chunk up the files.
			chunkSize := 2000 // characters.
			filenameToCostInformation := map[string]string{}
			totalCost := decimal.Decimal{}
			totalTokens := int64(0)
			files := []*store.File{}
			for _, filename := range filteredGitFiles {
				if !file.HasValidExtension(filename, config.EmbedFileExtensions) {
					continue
				}
				bytes, err := os.ReadFile(filename)
				if err != nil && strings.Contains(err.Error(), "is a directory") {
					continue
				}
				cobra.CheckErr(err)
				fileHash := fileHash(bytes)
				fileChunks := chunkFile(string(bytes), chunkSize)
				file := &store.File{
					Name:              filename,
					Hash:              fileHash,
					Chunks:            make([]*store.FileChunk, len(fileChunks)),
					CreationTimestamp: uint64(time.Now().Unix()),
				}
				if storeFile, ok := s.GetFile(file.Name); ok && storeFile.Hash == file.Hash && !opts.Force {
					continue
				}
				cli.AIOutput("Detected stale embeddings for file %s\n", file.Name)
				files = append(files, file)
				for i, chunk := range fileChunks {
					file.Chunks[i] = &store.FileChunk{
						Content:  chunk,
						Filename: filename,
					}
				}

				tokens, cost, err := model.CalculateEmbeddingCost(string(bytes))
				cobra.CheckErr(err)
				totalCost = totalCost.Add(cost)
				totalTokens += tokens
				filenameToCostInformation[file.Name] = fmt.Sprintf("%d tokens costing $%s\n", tokens, cost.String())
			}
			if len(files) == 0 {
				cli.AIOutput("All embeddings are up to date")
				return
			}

			cli.CostInfo("regenerating all embeddings (%d tokens) will cost: %s\n", totalTokens, totalCost.String())
			if !cli.QueryUser("Continue") {
				return
			}

			// Get embeddings from open ai.
			barrier := make(chan struct{}, 20)
			wg := &sync.WaitGroup{}
			wg.Add(len(files))
			mutex := sync.Mutex{}
			ctx := context.Background()
			for _, file := range files {
				file := file
				fn := func() {
					// Go through the barrier.
					barrier <- struct{}{}
					defer func() { <-barrier }()
					defer wg.Done()
					for _, chunk := range file.Chunks {
						content := fmt.Sprintf("file %s: %s", file.Name, chunk.Content)
						embedding, err := Content(ctx, openAIClient, content)
						cobra.CheckErr(err)
						chunk.Embedding = embedding
					}
					mutex.Lock()
					defer mutex.Unlock()
					s.AddFile(file)
					err := s.Save()
					cobra.CheckErr(err)
					cli.CostInfo("generated embedding for %s: %s", file.Name, filenameToCostInformation[file.Name])
				}
				go fn()
			}
			wg.Wait()
		},
	}
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "For embeddings regeneration")
	return cmd
}

// LoadStore returns the store for this git repo.
func LoadStore(config *configuration.Config) (*store.Store, error) {
	pwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	id := strings.ReplaceAll(pwd, "/", "_")
	filepath, err := file.ExpandPath(config.EmbedDirectory + "/" + id)
	if err != nil {
		return nil, err
	}
	return store.Load(filepath)
}

// Content embeds contents.
func Content(ctx context.Context, openAIClient *openai.Client, content string) ([]float32, error) {
	request := openai.EmbeddingRequest{
		Input: []string{content},
		Model: openai.AdaEmbeddingV2,
	}
	response, err := openAIClient.CreateEmbeddings(ctx, request)
	if err != nil {
		return nil, err
	}
	return response.Data[0].Embedding, nil
}

func chunkFile(content string, chunkSize int) []string {
	chunks := make([]string, 0)
	for i := 0; i < len(content); i += chunkSize {
		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}
		chunks = append(chunks, content[i:end])
	}
	return chunks
}

func fileHash(content []byte) string {
	hash := md5.Sum(content)
	return hex.EncodeToString(hash[:])
}
