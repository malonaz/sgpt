---
title: TUI design standard
description: The sgpt TUI's visual and interaction doctrine as built — content over chrome, the semantic palette and glyph set, the column grid, the tab bar / timeline / input / status bar layout, how messages and tool calls render (one-row calls from tool.Summary, tight and folded runs, the review card), toasts, the key doctrine (keep bindings, never alt+digits, ctrl+c never destroys), modals, and the screenshot harness.
labels:
  topic: tui
---

# TUI design standard

## Principles

1. **Content over chrome.** No boxes around prose. Ink (Text > Muted >
   Subtle), the column grid and whitespace carry hierarchy. A border is a
   signal. In the timeline only the tool call under review is boxed. Modal
   borders encode meaning: Subtle = informational (help, info), Brand =
   focused input (picker), Warning = waiting on you (confirm, review card).
2. **Hue means state or identity, never decoration.** Brand (violet) is
   focus, selection and the user's voice. Warning is "waiting on you".
   Success, Danger, Agent, File and Info each mean one thing.
3. **Quiet until it matters.** Resolved and running work is one row. Only a
   call under review expands by default.
4. **Every key is discoverable in context.** The status bar shows the hints
   that apply now. alt+h shows everything. Hints and help come from
   `keymap.Binding` (`WithShort(..).Hint()`), never from hand-written strings.
   Help strings are short, verb-first and sentence case.
5. **Width-1 glyphs, no emoji anywhere in the UI.** Emoji widths vary between
   terminals and break alignment.
6. **Adaptive.** Colors come only from `styles.Color*` (light and dark
   variants via `compat.AdaptiveColor`; glamour picks its base via
   `compat.HasDarkBackground`). There are no hex literals outside
   `styles/palette.go`.

## Tokens (`cli/tui/styles/palette.go`, `cli/tui/format`)

- **Inks:** `ColorText`, `ColorMuted`, `ColorSubtle`. Raised background:
  `ColorSurface`. Hues: `ColorBrand`, `ColorInfo`, `ColorSuccess`,
  `ColorWarning`, `ColorDanger`, `ColorAgent`, `ColorFile`.
- **Glyphs:** `✓ ✗ ▶ ● ◌ ⊘ › │ ▌ ↳ … ·` (`Glyph*`). Thought `∴`, injected
  file `@`, braille spinner frames for live work.
- **Numbers:** `format.Price` (`$0.05`, `<$0.01`), `Tokens` (`4.3k`),
  `Duration` (`1m 12s`), `Age` (`2h`). Never format inline.
- **Section labels:** Muted bold in modals, Subtle in lists.

## Layout

```
 Chats   Rename pendingReview   ▶ audit agent  3›            ← tab bar

  ┃ rename pendingReview to awaitingVerdict                   ← user turn

    Renaming the type and the map that holds it.              ← prose (wrap ≤ 100 cols)

  ✓ 5 tool calls · Read 2 · Shell 2 · Search lores 1          ← folded run
  ✓ Shell  gofmt -l internal/                     exit 0      ← tool rows, flush
▌╭ ▶ Edit   internal/session/review.go ──────────────────╮    ← review card (selected)
▌│ @@ -4,7 +4,7 @@ …                                     │
▌╰───────────────────────────────────────────────────────╯
  ↳ from launcher  alt+u          ● audit  ✓ bisect  $0.25    ← agent strip (blank slot without agents)
╭────────────────────────────────────────────────────────╮
│ › Message sgpt…                                        │    ← input
╰────────────────────────────────────────────────────────╯
 ▶ Review · Edit   ctrl+j accept · alt+shift+r reject      opus-5 · 23% ctx · $0.05
```

- **Tab bar:** no background. The active tab is a Brand bold pill on Surface;
  the others are muted. State glyphs: `▶` Warning, `●` busy, `✓` done, `✗`
  failed. Sub-agents get an Agent `↳` prefix, indented by depth. Titles share
  the width (the longest shrinks first, down to 12 columns), then the bar
  scrolls around the active tab. Overflow markers carry a count (`‹7`,
  `3›`) and turn Warning when a hidden tab awaits review.
- **Status bar:** one line, rendered by the app for every screen through
  `screen.StatusReporter`.
  - **Left:** turn state. `Ready`, or a spinner with `Thinking` /
    `Streaming` / `Running <Verb>` and the elapsed time, or Warning `▶ Review
    · <Verb>`. Then `↓ N more` when scrolled up, then hints, with `alt+h help`
    last.
  - **Right:** facts by priority: `N% ctx` (Warning ≥75%, Danger ≥90%) >
    model > cost (hidden at $0) > reasoning (when set) > role (when not the
    default). Lowest-priority facts and extra hints drop as width shrinks;
    the state is always kept.
- **Toasts** (`screen.AlertMsg{Level}`) replace the hints in place, so the
  layout never moves. Errors show `✗` in Danger for 6s; success `✓` and info
  last 2.5s. They're queued, never lost, and always one line.
- **Input:** rounded border, Brand when focused and Subtle when blurred (the
  focus cue). A `›` prompt. Placeholders: `Message sgpt…`, `Queue a
  message…` mid-turn, `Message, or a reason to reject…` in review. Height
  follows soft-wrapped rows, from 1 line up to 20, never squeezing the
  timeline below 4 rows.
- **Layout math is exact:** every screen renders exactly its `SetSize`
  height and width.

## Timeline

- **Column grid:** columns 0–1 are the selection gutter (Brand `▌` at col 0
  on every line of the selected item while the timeline has focus; Subtle on
  sibling fences). Columns 2–3 hold the marker (`┃`, state glyph, `∴`,
  `@`). Text starts at col 4. The review card's border sits at col 1, with
  its row riding in the top border.
- **Spacing:** one blank line between items. One-line items (folded calls,
  thoughts, run rows) sit flush.
- **Items:**
  - User turn: Brand `┃` plus text.
  - Agent-sent message: Agent `┃`, `↳ sender`, muted note.
  - Assistant prose: plain markdown.
  - Thought: `∴ Thought · N lines`, expanding to muted text behind `│`.
  - System prompt, injected files, `◌ Queued`, `⊘ Turn cancelled`: one
    line each.
  - Error: `✗` in Danger.
- **Markdown:** prose wraps at `markdown.MaxProseWidth` (100); code and
  diffs use the full width. ```diff fences skip glamour: `+` Success, `-`
  Danger, headers muted. Glamour joins lines inside blockquotes, so use list
  items for line-oriented excerpts.

## Tool calls

- **One call is one row:** `glyph Verb Subject  display… Outcome`, with the
  outcome right-aligned and the row capped at prose width. It's rendered
  without glamour.
  - The text comes from `tool.Summarizer` → `tool.Summary{Verb, Subject,
    Outcome, Failed}`.
  - `Registry.Summarize` derives the outcome of error results itself (first
    line, `Failed`).
  - `Failed` also marks failures the call didn't report (`exit 1`): Danger
    `✗`.
  - Fallback: humanized tool name, first one-line string argument, error
    text.
- **Verbs are API** (runs group by them): Read, View, Shell,
  Edit/Create/Delete/Rename, Search lores, Launch agents, Message, Check
  agents, Wait, Cancel, Discover, plus humanized RPC methods.
- **Folded runs:** more than 3 resolved calls, plus the folded thoughts
  between them, become `✓ N tool calls · Verb n · …` (with `· k failed` in
  Danger). Folding happens in `Model.SetItems`.
  - A run at the end of the timeline doesn't fold until something follows
    it, so there's no flicker mid-turn.
  - The run id is `run-` + its first child's id, which keeps the cursor and
    collapse state stable.
- **Expanded call** (enter or alt+z): body behind a subtle `│`. Display
  message, then the request (the tool's `RequestRenderer`, else nested
  `key: value`, never JSON braces), then the result (`ResultRenderer`, else
  compact text). Capped at 12 lines with `… N more lines · alt+o open`.
- **Review card:** the only boxed timeline element (Warning, rounded) for own
  and escalated calls. It holds the display message and the full,
  untruncated request. The action hints live in the status bar, not the
  card.
- **Running/streaming calls** stay one row with a spinner.

## Keys

- **Keep the existing bindings** (muscle memory, window manager).
- **Never bind alt+digits.** They conflict with the user's tab navigation.
  Tab jumps stay on alt+F1..F9.
- **Additions in use:**
  - esc: back to the input / to search.
  - pgup/pgdn: scroll the timeline from anywhere; page the help modal.
  - enter (timeline focus): toggle expand.
  - Mouse wheel: scroll. Cell-motion mouse mode; shift+drag keeps native
    selection.
- **ctrl+c never destroys** (`screen.Interrupter`), in this order:
  1. Close an open modal.
  2. Cancel the turn.
  3. Clear the input.
  4. Show the toast "ctrl+c again to quit".
  5. A second press within 1.5s opens the quit confirm.

  ctrl+w is the only key that closes a tab.
- **Destructive actions confirm** via `screen.ConfirmMsg`.
- **Modals:** esc closes; the help modal pages with pgup/pgdn; any other key
  closes. They always fit the screen (balanced columns, then pages). Modal
  hints show the primary key only.

## Chats tab

- Search is case-insensitive and client-side over title and chat id. While a
  filter is active, the remaining pages load in the background
  ("Searching…" → "N matches").
- Server-side title filters aren't used: core's AIP transpiler is
  case-sensitive (`LIKE`). Revisit if it gains `ILIKE`.

## Verifying

`plz build //docs/screenshots`, then from `docs/screenshots/` run
`../../plz-out/bin/docs/screenshots/screenshots -out /tmp/frames -text
[-scene NAME] [-width 80 -height 24]`.

- It drives the real App against an in-process fake AiService, writing
  `.ansi` (colors) and `.txt` (stripped) frames.
- Each area registers its scenes from its own `scenes_*.go` via
  `register(...)`.
- Scenes share one fixture copy per run, so don't apply the same edit
  twice.
- Check 80×24, 120×36 and 160×45. `render.sh` regenerates the site SVGs
  (needs `freeze`).
