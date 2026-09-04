---
title: Installation
description: Install the sgpt binary and shell completions.
---

## With Go

```bash
go install github.com/malonaz/sgpt/cmd/sgpt-cli@latest
mv "$(go env GOPATH)/bin/sgpt-cli" ~/.local/bin/sgpt
```

Make sure `~/.local/bin` is on your `PATH`.

## From source (please)

The repo builds with [please](https://please.build):

```bash
git clone https://github.com/malonaz/sgpt && cd sgpt
plz build //cmd/sgpt-cli
./scripts/install.sh        # moves the binary to ~/.local/bin/sgpt, installs completions
```

## Shell completions

```bash
sgpt completion bash > ~/.local/share/bash-completion/completions/sgpt
sgpt completion zsh  > ~/.zfunc/_sgpt
sgpt completion fish > ~/.config/fish/completions/sgpt.fish
```

Completions cover roles, models (including aliases), tool names and chats.

## Requirements

- A reachable `AiService` gRPC backend and an API key for it.
- A terminal with alt/meta key support (most do; on macOS Terminal enable
  *Use Option as Meta key*).
- Optional: `$EDITOR` for composing long messages and viewing chats.
