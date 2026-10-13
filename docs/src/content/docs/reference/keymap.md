---
title: Keymap
description: Every key binding, by context. alt+h shows this in the TUI.
---

Bindings use <kbd>alt</kbd> for actions and <kbd>ctrl</kbd> for the few
that must never collide with typing. <kbd>alt</kbd>+<kbd>h</kbd> toggles an
in-app cheat sheet.

## Global

| Key | Action |
|---|---|
| <kbd>alt</kbd>+<kbd>h</kbd> | Toggle help (<kbd>pgup</kbd> / <kbd>pgdn</kbd> page it) |
| <kbd>ctrl</kbd>+<kbd>c</kbd> | Quit (press twice) |
| <kbd>ctrl</kbd>+<kbd>t</kbd> | New tab |
| <kbd>ctrl</kbd>+<kbd>w</kbd> | Close tab |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>w</kbd> | Close idle sub-agent tabs |
| <kbd>alt</kbd>+<kbd>j</kbd> | Previous tab |
| <kbd>alt</kbd>+<kbd>;</kbd> | Next tab |
| <kbd>alt</kbd>+<kbd>F1</kbd>…<kbd>F9</kbd> | Jump to tab 1 to 9 |
| <kbd>alt</kbd>+<kbd>m</kbd> | Open menu |
| <kbd>alt</kbd>+<kbd>c</kbd> | Copy chat name |

## Chat

| Key | Action |
|---|---|
| <kbd>ctrl</kbd>+<kbd>j</kbd> | Send, or accept the review |
| <kbd>alt</kbd>+<kbd>y</kbd> | Accept the tool call |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>y</kbd> | Accept all tool calls |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>a</kbd> | Always accept this tool |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>r</kbd> | Reject (input is the reason) |
| <kbd>ctrl</kbd>+<kbd>c</kbd> | Cancel turn, or clear input |
| <kbd>tab</kbd> | Switch input and timeline |
| <kbd>esc</kbd> | Back to the input |
| <kbd>pgup</kbd> | Page up |
| <kbd>pgdn</kbd> | Page down |
| mouse wheel | Scroll (<kbd>shift</kbd>+drag selects text) |
| <kbd>alt</kbd>+<kbd>t</kbd> | Cycle reasoning effort |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>f</kbd> | Toggle favorite |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>o</kbd> | Open chat in `$EDITOR` |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>t</kbd> | Pick tools |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>e</kbd> | Pick files |
| <kbd>alt</kbd>+<kbd>d</kbd> | Delete message |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>d</kbd> | Delete message and below |
| <kbd>alt</kbd>+<kbd>e</kbd> | Edit and resend message |
| <kbd>alt</kbd>+<kbd>i</kbd> | Chat info |
| <kbd>alt</kbd>+<kbd>u</kbd> | Go to launcher |
| <kbd>alt</kbd>+<kbd>g</kbd> | Go to next sub-agent |

## Input

| Key | Action |
|---|---|
| <kbd>enter</kbd> | Newline |
| <kbd>alt</kbd>+<kbd>p</kbd> | Previous history entry |
| <kbd>alt</kbd>+<kbd>n</kbd> | Next history entry |
| <kbd>alt</kbd>+<kbd>o</kbd> | Compose in `$EDITOR` |

## Timeline

| Key | Action |
|---|---|
| <kbd>alt</kbd>+<kbd>[</kbd> | Previous fence or block |
| <kbd>alt</kbd>+<kbd>]</kbd> | Next fence or block |
| <kbd>alt</kbd>+<kbd>&lt;</kbd> | Jump to top |
| <kbd>alt</kbd>+<kbd>&gt;</kbd> | Jump to bottom |
| <kbd>ctrl</kbd>+<kbd>p</kbd> | Scroll up |
| <kbd>ctrl</kbd>+<kbd>n</kbd> | Scroll down |
| <kbd>alt</kbd>+<kbd>z</kbd> / <kbd>enter</kbd> | Expand or collapse |
| <kbd>alt</kbd>+<kbd>a</kbd> | Toggle fence or block steps |
| <kbd>alt</kbd>+<kbd>w</kbd> | Copy selection |
| <kbd>alt</kbd>+<kbd>o</kbd> | Open selection in `$EDITOR` |

## Menu

| Key | Action |
|---|---|
| <kbd>ctrl</kbd>+<kbd>p</kbd> | Move up |
| <kbd>ctrl</kbd>+<kbd>n</kbd> | Move down |
| <kbd>pgup</kbd> | Page up |
| <kbd>pgdn</kbd> | Page down |
| <kbd>enter</kbd> | Open chat |
| <kbd>esc</kbd> | Back to search |
| <kbd>alt</kbd>+<kbd>d</kbd> | Delete chat |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>f</kbd> | Toggle favorite |
| <kbd>alt</kbd>+<kbd>s</kbd> | Show or hide sub-agents |
| <kbd>alt</kbd>+<kbd>r</kbd> | Refresh |
| <kbd>alt</kbd>+<kbd>&lt;</kbd> | Jump to search |
| <kbd>alt</kbd>+<kbd>&gt;</kbd> | Jump to last chat |
