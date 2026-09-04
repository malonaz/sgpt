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
| <kbd>ctrl</kbd>+<kbd>t</kbd> | New chat tab |
| <kbd>ctrl</kbd>+<kbd>w</kbd> | Close tab |
| <kbd>alt</kbd>+<kbd>j</kbd> / <kbd>alt</kbd>+<kbd>;</kbd> | Previous / next tab |
| <kbd>alt</kbd>+<kbd>m</kbd> | Open the chat menu |
| <kbd>alt</kbd>+<kbd>c</kbd> | Copy the chat's resource name |
| <kbd>alt</kbd>+<kbd>h</kbd> | Toggle help |
| <kbd>ctrl</kbd>+<kbd>c</kbd> | Cancel stream / close tab / quit |

## Chat

| Key | Action |
|---|---|
| <kbd>ctrl</kbd>+<kbd>j</kbd> | Send message, or review the tool call under the cursor |
| <kbd>tab</kbd> | Toggle focus between input and timeline |
| <kbd>alt</kbd>+<kbd>y</kbd> | Accept tool call under review |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>y</kbd> | Accept all pending tool calls |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>a</kbd> | Always accept this tool (session) |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>r</kbd> | Reject tool call; input text is the reason |
| <kbd>alt</kbd>+<kbd>t</kbd> | Cycle reasoning effort (low → medium → high) |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>t</kbd> | Fuzzy-pick tools |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>e</kbd> | Fuzzy-pick injected files |
| <kbd>alt</kbd>+<kbd>i</kbd> | Chat info: context, tokens, cost |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>f</kbd> | Toggle favourite |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>o</kbd> | Open the whole chat in `$EDITOR` |
| <kbd>alt</kbd>+<kbd>d</kbd> | Delete selected message |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>d</kbd> | Delete selected message and everything below |
| <kbd>alt</kbd>+<kbd>e</kbd> | Edit selected user message and resend (truncates below) |

## Input

| Key | Action |
|---|---|
| <kbd>enter</kbd> | Newline |
| <kbd>alt</kbd>+<kbd>p</kbd> / <kbd>alt</kbd>+<kbd>n</kbd> | Previous / next history entry |
| <kbd>alt</kbd>+<kbd>o</kbd> | Compose in `$EDITOR` |

## Timeline

| Key | Action |
|---|---|
| <kbd>ctrl</kbd>+<kbd>p</kbd> / <kbd>ctrl</kbd>+<kbd>n</kbd> | Scroll up / down |
| <kbd>alt</kbd>+<kbd>[</kbd> / <kbd>alt</kbd>+<kbd>]</kbd> | Previous / next block |
| <kbd>alt</kbd>+<kbd>&lt;</kbd> / <kbd>alt</kbd>+<kbd>&gt;</kbd> | Jump to top / bottom |
| <kbd>alt</kbd>+<kbd>a</kbd> | Toggle navigation between code fences and API blocks |
| <kbd>alt</kbd>+<kbd>z</kbd> | Collapse / expand the item |
| <kbd>alt</kbd>+<kbd>w</kbd> | Copy selection to clipboard |
| <kbd>alt</kbd>+<kbd>o</kbd> | Open selection in `$EDITOR` |

## Menu

| Key | Action |
|---|---|
| <kbd>ctrl</kbd>+<kbd>p</kbd> / <kbd>ctrl</kbd>+<kbd>n</kbd> | Move up / down |
| <kbd>enter</kbd> | Open chat |
| <kbd>alt</kbd>+<kbd>d</kbd> | Delete chat |
| <kbd>alt</kbd>+<kbd>r</kbd> | Refresh |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>f</kbd> | Toggle favourite |
| <kbd>alt</kbd>+<kbd>&lt;</kbd> / <kbd>alt</kbd>+<kbd>&gt;</kbd> | Jump to filter / last chat |
