package embed

import (
	"context"
	"time"
	"os"
	"bytes"
	"os/exec"
	"strings"
	"crypto/md5"
	"fmt"
	"encoding/hex"

	"github.com/shopspring/decimal"
	"github.com/AlecAivazis/survey/v2"
	"github.com/fatih/color"
	"github.com/pkg/errors"
	"github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"
	"github.com/malonaz/sgpt/configuration"
	"github.com/malonaz/sgpt/model"
	"github.com/malonaz/sgpt/file"
	"github.com/malonaz/sgpt/embed/store"
)

const (
	asciiSeparator       = "----------------------------------------------------------------------------------------------------------------------------------"
	asciiSeparatorInject = "-------------------------------------------------Embed [%s]---------------------------------------------------\n"
)

// NewCmd instantiates and returns the diff command.
func NewCmd(openAIClient *openai.Client, config *configuration.Config) *cobra.Command {
	var opts struct {
	}
	_ = opts
	cmd := &cobra.Command{
		Use:   "embed",
		Short: "Generate embeddings for a repo",
		Long: "Generate embeddings for a repo",
		Args:  cobra.ExactArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			// Set the model.
			optsModel := &model.Opts{ Model: "text-embedding-ada-002" }
			model, err := model.Parse(optsModel, config)
			cobra.CheckErr(err)

			pwd, err := os.Getwd()
			cobra.CheckErr(err)
			id := strings.ReplaceAll(pwd, "/", "_")
			filepath, err := file.ExpandPath("~/.sgpt/embed/" + id)
			cobra.CheckErr(err)
			s, err := store.Load(filepath)
			cobra.CheckErr(err)

			// Colors.
			fileColor := color.New(color.FgRed)
			aiColor := color.New(color.FgCyan)
			formatColor := color.New(color.FgGreen)
			costColor := color.New(color.FgYellow)
			// Print title.
			formatColor.Println(asciiSeparator)
			formatColor.Printf(asciiSeparatorInject, model.ID)
			formatColor.Println(asciiSeparator)

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
						fileColor.Printf("Ignoring %s\n", gitFile)
						ignore = true
						break
					}
				}
				if !ignore {
					filteredGitFiles = append(filteredGitFiles, gitFile)
				}
			}
			formatColor.Println(asciiSeparator)

			// Chunk up the files.
			chunkSize := 1000 // characters.
			filenameToCostInformation := map[string]string{}
			totalCost := decimal.Decimal{}
			totalTokens := int64(0)
			files := []*store.File{}
			for _, filename := range filteredGitFiles {
				bytes, err := os.ReadFile(filename)
				cobra.CheckErr(err)
				fileHash := fileHash(bytes)
				fileChunks := chunkFile(string(bytes), chunkSize)
				file := &store.File{
					Name:             filename,
					Hash:             fileHash,
					Chunks:  make([]*store.FileChunk, len(fileChunks)),
					CreationTimestamp: uint64(time.Now().Unix()),
				}
				if storeFile, ok := s.GetFile(file.Name); ok && storeFile.Hash == file.Hash {
					timeSinceGeneration := time.Now().Sub(time.Unix(int64(storeFile.CreationTimestamp), 0))
					aiColor.Printf("skipping file %s which was generated %s ago\n", file.Name, timeSinceGeneration.String())
					continue
				}
				files = append(files, file)
				for i, chunk := range fileChunks {
					file.Chunks[i] = &store.FileChunk{
						Content: chunk,
						Filename: filename,
					}
				}

				tokens, cost, err := model.CalculateEmbeddingCost(string(bytes))
				cobra.CheckErr(err)
				totalCost = totalCost.Add(cost)
				totalTokens += tokens
				filenameToCostInformation[file.Name] = fmt.Sprintf("File contains %d tokens costing $%s\n", tokens, cost.String())
			}
			if len(files) == 0 {
				aiColor.Println("all embeddings are up to date")
				return
			}
			costColor.Printf("regenerating all embeddings (%d tokens) will cost: %s\n", totalTokens, totalCost.String())
			formatColor.Println(asciiSeparator)
			// Check if user wants to commit the message.
			surveyQuestion := &survey.Confirm{
				Message: "Continue",
			}
			confirm := false
			survey.AskOne(surveyQuestion, &confirm)
			if !confirm {
				return
			}


			// Get embeddings from open ai.
			ctx := context.Background()
			for _, file := range files {
				aiColor.Printf("generating embedding for %s: %d chunks\n", file.Name, len(file.Chunks))
				costInformation := filenameToCostInformation[file.Name]
				costColor.Println(costInformation)
				formatColor.Println(asciiSeparator)
				for _, chunk := range file.Chunks {
					request := openai.EmbeddingRequest{
						Input: []string{chunk.Content},
						Model: openai.AdaEmbeddingV2,
					}
					response, err := openAIClient.CreateEmbeddings(ctx, request)
					cobra.CheckErr(err)
					chunk.Embedding = response.Data[0].Embedding
				}
				s.AddFile(file)
				err := s.Save()
				cobra.CheckErr(err)
			}
		},
	}
	return cmd
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
