---
title: First chat
description: A five-minute tour of the TUI.
---

```bash
cd ~/my-repo
sgpt chat
```

You land in a new chat tab. The bottom pane is the input; the pane above it
is the **timeline** — the conversation rendered as markdown.

## Send a message

Type, then press <kbd>ctrl</kbd>+<kbd>j</kbd>. Newlines are plain
<kbd>enter</kbd>, so multi-line prompts are natural. For anything long,
<kbd>alt</kbd>+<kbd>o</kbd> opens `$EDITOR`.

## Watch a tool call

Ask for something that needs the filesystem:

> what does `internal/session/turn.go` do?

The model calls `read_files`. Reads are side-effect free, so the call runs
immediately and its result streams back. Now ask for a change:

> rename `pendingReview` to `awaitingVerdict`

The model proposes an edit. The timeline shows a **unified diff** and the
status line says a review is pending:

- <kbd>alt</kbd>+<kbd>y</kbd> accept
- <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>r</kbd> reject — whatever is in the
  input box is sent back as the reason
- <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>a</kbd> always accept this tool for
  the rest of the session

## Inject files

`sgpt chat -f internal/session/... -f go.mod` puts files in the context up
front (`dir/...` recurses, `dir` is one level). Inside a chat,
<kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>e</kbd> opens a fuzzy picker to add or
remove files; <kbd>alt</kbd>+<kbd>i</kbd> shows what the context holds and
what it costs.

## Pick a role and model

```bash
sgpt chat -r reviewer -m opus
```

Roles come from `.sgpt/*.role.md` files in the repo (and its imports); see
[Roles](/concepts/roles/). Models are resource names or aliases.

## Tabs and the menu

- <kbd>ctrl</kbd>+<kbd>t</kbd> new tab, <kbd>ctrl</kbd>+<kbd>w</kbd> close,
  <kbd>alt</kbd>+<kbd>j</kbd> / <kbd>alt</kbd>+<kbd>;</kbd> switch.
- <kbd>alt</kbd>+<kbd>m</kbd> opens the **menu**: every chat for your user,
  filterable, with favourites (<kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>f</kbd>).
- `sgpt chat -c` reopens the last chat; `sgpt chat --name <chat>` a specific
  one.

<kbd>alt</kbd>+<kbd>h</kbd> shows the full [keymap](/reference/keymap/) at
any time.
