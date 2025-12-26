#!/usr/bin/env bash

GREEN='\033[0;32m'
NC='\033[0m'

detect_shell() {
    if [ -n "$BASH_VERSION" ]; then
        echo "bash"
    elif [ -n "$ZSH_VERSION" ]; then
        echo "zsh"
    elif [ -n "$FISH_VERSION" ]; then
        echo "fish"
    elif [ -n "$SHELL" ]; then
        case "$SHELL" in
            */bash) echo "bash" ;;
            */zsh) echo "zsh" ;;
            */fish) echo "fish" ;;
            *) echo "unknown" ;;
        esac
    else
        echo "unknown"
    fi
}

if [ $# -eq 0 ]; then
    SHELL_TYPE=$(detect_shell)
    if [ "$SHELL_TYPE" = "unknown" ]; then
        echo "Error: Could not auto-detect shell type"
        echo "Usage: $0 <bash|zsh|fish>"
        exit 1
    fi
    echo "Auto-detected shell: $SHELL_TYPE"
else
    SHELL_TYPE="$1"
fi

case "$SHELL_TYPE" in
    bash|zsh|fish) ;;
    *)
        echo "Error: Invalid shell type '$SHELL_TYPE'"
        echo "Supported shells: bash, zsh, fish"
        exit 1
        ;;
esac

mkdir -p ~/.local/bin

src="plz-out/bin/cmd/sgpt/sgpt"
dst="$HOME/.local/bin/sgpt"

if [ "$src" -ef "$dst" ]; then
    echo -e "${GREEN}✓${NC} Binary already at ~/.local/bin/sgpt"
else
    mv "$src" "$dst"
    echo -e "${GREEN}✓${NC} Installed binary to ~/.local/bin/sgpt"
fi

case "$SHELL_TYPE" in
    bash)
        mkdir -p ~/.local/share/bash-completion/completions
        ~/.local/bin/sgpt --local completion bash > ~/.local/share/bash-completion/completions/sgpt
        echo -e "${GREEN}✓${NC} Installed completions to ~/.local/share/bash-completion/completions/sgpt"
        ;;
    zsh)
        mkdir -p ~/.zfunc
        ~/.local/bin/sgpt --local completion zsh > ~/.zfunc/_sgpt
        echo -e "${GREEN}✓${NC} Installed completions to ~/.zfunc/_sgpt"
        echo -e "${GREEN}Note:${NC} Add to ~/.zshrc: fpath=(~/.zfunc \$fpath); autoload -Uz compinit && compinit"
        ;;
    fish)
        mkdir -p ~/.config/fish/completions
        ~/.local/bin/sgpt --local completion fish > ~/.config/fish/completions/sgpt.fish
        echo -e "${GREEN}✓${NC} Installed completions to ~/.config/fish/completions/sgpt.fish"
        ;;
esac

echo -e "\n${GREEN}Note:${NC} Add ~/.local/bin to your PATH:"
echo '  export PATH="$HOME/.local/bin:$PATH"'
echo "Add this line to your shell config, then restart your shell."
