# SGPT - Shell GPT
SGPT is a Go-native command-line interface (CLI) for OpenAI's Language Models. It is designed to be developer-friendly, making it easy to interact with OpenAI's API. SGPT offers you the ability to conduct a back-and-forth chat with the API, and even inject file content into the context.
## Configuration
SGPT uses a configuration file to manage various settings. The configuration file is a JSON that includes information such as the OpenAI API Key, the API host, request timeout, the default model, and the chat directory. This resides in a default directory `~/.sgpt/config.json`.
Here's a sample default configuration:
```json
{
    "openai_api_key": "API_KEY",
    "openai_api_host": "https://api.openai.com",
    "request_timeout": 60,
    "default_model": "gpt-3.5-turbo",
    "chat_directory": "~/.sgpt/chats"
}
```
You can edit this file to personalize the parameters according to your requirements.
## Commands
Once you have SGPT installed and the configuration is set up, you can interact with the OpenAI's Language Models using the following command:
### `sgpt chat`
This command initiates a back-and-forth chat. You can specify the model, chat id, file content to inject into the context, and more.
**Command options:**
- `--model`: Override the default OpenAI model
- `--id` : Specify a chat id. If none is supplied, a temporary chat session is created that will be not be persisted to disk.
- `--file` : Specify files whose content should be injected into the context.
- `--ext` : Specify file extensions to accept (used in conjunction with the --file flag).
- `--code` : If given, prints code
- `--no_role` : If given, does not inject a role into the context
For example:
```bash
sgpt chat --model gpt-4 --id my_chat
```
This initiates a chat using the gpt-4 model and the id 'my_chat'.
**Please be aware that chat sessions are saved to disk at the chat directory specified in the configuration (defaulted to ~/.sgpt/chats).**
You can read the chat by using the id of the chat, which is listed in the chat directory.
SGPT is designed to make the developer's interaction with OpenAI's Language Models as seamless as possible. Feel free to contribute or report issues as you use SGPT.
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
## Getting Started
To get started with SGPT, you'll need to have [Go](https://golang.org/dl/) installed on your machine. Then, use `go get` to fetch the package. Configure your OpenAI API Key and any other configuration parameters you wish to adjust, and you're good to go!
## Contributing
Contributions to SGPT are welcome! Whether it's feature requests, bug fixes, documentation improvements, or any other changes, we are glad to see them.
