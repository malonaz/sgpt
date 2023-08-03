# SGPT - Shell GPT
> "Amplify your coding capabilities with AI - your smart co-pilot for an elevated coding experience."

> "Generate embeddings for your git repo, unleashing the power of AI-driven analysis and insights."

SGPT is a bespoke tool designed by developers, for developers. It acts as a copilot, helping you write, optimize, or debug your code. Leveraging OpenAI's Language Models through an intuitive command-line interface, SGPT brings the power of AI right to your terminal. With the ability to inject file content into your chats, or generate and search embeddings for your code, SGPT promotes an elevated coding experience.

## Getting Started
To get started with SGPT, you'll need to have [Go](https://golang.org/dl/) installed on your machine. You can then fetch and install SGPT via the command line using the following command:

```bash
go install github.com/malonaz/sgpt/cmd/sgpt
```
After installation, configure your OpenAI API Key and any other configuration parameters you wish to adjust, and you're good to go!

## Commands
Once you have SGPT installed and the configuration is set up, you can interact with the OpenAI's Language Models using the following command:
### `sgpt chat`
This command initiates a back-and-forth chat. You can specify the model, chat id, file content to inject into the context, user role, embeddings usage, and more. In a chat session initiated by the `sgpt chat` command, SGPT uses the 'Ctrl+J' key combination to detect the end of user input.
This means that you can continue to input multiple lines of chat, and only when you're finished and want to send the chat context to the AI for generation, you would press 'Ctrl+J'. The program will then recognize this as the conclusion of the current user input block.
**Command options:**
- `--model`: Override the default OpenAI model
- `--id` : Specify a chat id. If none is supplied, a new chat session with a generated id is created and persisted to disk.
- `--file` : Specify files whose content should be injected into the context.
- `--ext` : Specify file extensions to accept (used in conjunction with the --file flag).
- `--role` : Specify a user role that the AI will emulate. Available roles: `code`, `shell`.
- `--embeddings` : Use repository embeddings to augment AI response if matching content is found (generated with `sgpt embed`).
For example:
```bash
sgpt chat --model gpt-3.5-turbo --id my_chat --role code
```
This initiates a chat using the gpt-3.5-turbo model, the id 'my_chat', and the AI will play the role of providing only code as output.
**Please note that chat sessions are saved to disk at the chat directory specified in the configuration (defaulted to ~/.config/sgpt/chats).**
You can read the chat by using the id of the chat, which is listed in the chat directory.

### `sgpt diff`
The `sgpt diff` command helps you generate a Git commit message based on the staged changes in your Git repository. The command reads the staged changes, excludes specified files, and generates a commit message using OpenAI's GPT model.
**Command options:**
- `--model`: Override the default OpenAI model
- `--message`/`-m`: Give extra instruction to sgpt diff.
```bash
sgpt diff --model gpt-3.5-turbo --message "Focus on the frontend changes"
```
This command generates a git commit message for the staged changes using the gpt-3.5-turbo model, and gives it extra instructions to "Focs on the frontend changes".
**Note:**
- Before running `sgpt diff`, make sure you have staged the changes you want to commit using `git add`.
- You can exclude specific files from being considered while generating the commit message by adding them to the `DiffIgnoreFiles` list in your `config.json`. This is useful to ignore autogen files, such as `.wollemi.json` in this repo, which pollute the sgpt diff context and do not provide relevant information to `sgpt diff`.
- The `sgpt diff` command requires Git to be installed and available in your system's PATH.

### `sgpt embed`
The `sgpt embed` command is used to generate embeddings for a repository. This is an efficient way to represent a repository's code and other content in a way that can be easily processed and compared by machine learning models like GPT.
To use the `sgpt embed` command, simply run:
```bash
sgpt embed
```
The command will recursively analyze all the files in the current repository, generate embeddings for each individual file, and save the generated embeddings to a local store. You can re-run it many times; it will only regenerate embeddings for files that have changed since the last time.
By default, the `sgpt embed` command uses the "text-embedding-ada-002" model and cannot be overridden. Additionally, you can use the `--force` flag to force embeddings regeneration for all files even if they haven't changed:
```bash
sgpt embed --force
```

## Configuration
SGPT uses a configuration file to manage various settings. The configuration file is a JSON that includes information such as the OpenAI API Key, the API host, request timeout, the default model, and the chat directory. This resides in a default directory `~/.config/sgpt/config.json`.
Here's a sample default configuration:
```json
{
    "openai_api_key": "API_KEY",
    "openai_api_host": "https://api.openai.com",
    "request_timeout": 60,
    "default_model": "gpt-3.5-turbo",
    "chat_directory": "~/.config/sgpt/chats"
}
```
You can edit this file to personalize the parameters according to your requirements.

## Contributing
Contributions to SGPT are welcome! Whether it's feature requests, bug fixes, documentation improvements, or any other changes, we are glad to see them.

## Using the `--file` flag
The `--file` flag is an integral part of the `sgpt chat` command used to inject file content into the chat context. This flag can be used in various ways to achieve broad control over file inputs.
1. **Specifying multiple files:** You can specify multiple files by using the `--file` flag multiple times in the command:
    Example:
    ```bash
    sgpt chat --file=path/to/file1 --file=path/to/file2
    ```
    In this case, the content of `file1` and `file2` will be injected into the chat context.
2. **Directory Recursion:** You can provide directory paths to the `--file` flag with a `/...` suffix. This suffix indicates to SGPT that it should recurse into the specified directory, injecting the content of all files found within.
    Example:
    ```bash
    sgpt chat --file=path/to/directory/...
    ```
    This command will inject the content of all files in the given directory and its sub-directories into the chat context.
3. **One-Level Directory Capture:** If you only want to capture the files in a particular directory without any recursion into sub-directories, specify the directory path without any suffix.
    Example:
    ```bash
    sgpt chat --file=path/to/directory
    ```
    In this case, the content of all files in the given directory (and not its sub-directories) will be injected into the chat context.
## Specifying File Extensions with `--ext`
If you want to limit files by their extensions, you can use the `--ext` flag to specify the valid extensions. This can be particularly useful when you're dealing with directories and want to target specific types of files.
Example:
```bash
sgpt chat --file=path/to/directory --ext=.txt
```
In this case, only the `.txt` files in the specified directory will have their content injected into the chat context. You can also specify multiple extensions by using the `--ext` flag multiple times in the command:
```bash
sgpt chat --file=path/to/directory --ext=.txt --ext=.md
```
Now, both `.txt` and `.md` files are considered valid, and their content will be injected into the chat context.
These advanced usages of the `--file` and `--ext` flags make it easy for developers to customize the chat context based on their file inputs, enhancing SGPT's versatility and usability.

## TODOs
- Give the ability to interrupt AI.-
