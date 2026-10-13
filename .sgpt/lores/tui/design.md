---
title: TUI design standard
description: The sgpt TUI's visual and interaction doctrine as built — readability through structure (turn bands, finished-turn folding, title-first tool rows), the semantic palette with background-derived surfaces, the column grid, selection vs user-turn treatment, the status bar and toasts, the key doctrine (keep bindings, never alt+digits, ctrl+c one press one effect, tab/history/copy are critical), editor documents, and the screenshot harness.
labels:
  topic: tui
---

# TUI design standard

## Principles

1. **Readable through structure, in one left-aligned column.** Turns read as
   sections: your message is a raised band, then the agent's work collapses to
   one row, then the answer. Hierarchy comes from ink (Text > Muted), rhythm and
   bands, not boxes. The tool call under review is the only boxed timeline
   element.
2. **Hue means state or identity.** Brand (violet) is focus, selection and
   your voice. Warning is "waiting on you". Success, Danger, Agent, File and
   Info each mean one thing.
3. **Quiet until it matters.** A finished turn's work is one row; a live turn
   shows its steps; only a call under review expands by default.
4. **Every key is discoverable in context.** The status bar shows the hints
   that apply now; alt+h shows everything. Hints and help come from
   `keymap.Binding` (`WithShort(..).Hint()`). Help strings are short,
   verb-first and sentence case.
5. **Tab, input history and copy-paste are critical.** Never capture the mouse
   (native selection must work). Never put glyphs inside copyable bodies.
6. **Width-1 glyphs, no emoji anywhere in the UI.**

## Tokens

- **Inks:** `ColorText` (content), `ColorMuted` (secondary text; about 5.7:1
  on #282a36), `ColorSubtle` (**never text**: rules, separators, glyphs,
  borders only).
- **Surfaces** (`styles/surface.go`): `ColorSurface` (inline code, the active
  tab, agent messages), `ColorSurfaceStrong` (selection) and
  `ColorSurfaceTurn` (user turns, Brand-tinted) are lifted off the
  terminal's real background. The app requests it (`tea.RequestBackgroundColor`
  → `styles.SetBackground`), then every `screen.Restyler` rebuilds its
  color-baked caches. Fixed shades are the fallback until then.
- **Hues:** `ColorBrand`, `ColorInfo`, `ColorSuccess`, `ColorWarning`,
  `ColorDanger`, `ColorAgent`, `ColorFile`. No hex literals outside `styles/`.
- **Glyphs:** `✓ ✗ ▶ ● ◌ ⊘ › ▌ ↳ … ·`, plus thought `∴`, injected file `@` and
  braille spinner frames. `▌` is the only vertical bar, and it means
  selection.
- **Numbers:** `format.Price`, `Tokens`, `Duration`, `Age`.
- **Light theme: not yet tuned.** Strings pre-rendered at package level bake
  in their color before the background is known; fix those when the light
  theme is done.

## Layout

```
 Chats   Rename pendingReview   ▶ audit agent  3›             ← tab bar


 ▒› rename pendingReview to awaitingVerdict▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒   ← user turn (Surface band)

  ✓ Worked 2m 13s · 11 tool calls · Shell 5 · Read 2 · 1 failed  ← finished work (one row)

    Renamed pendingReview to awaitingVerdict…                  ← answer
╭──────────────────────────────────────────────────────────╮
│ › Message sgpt…                                          │   ← input
╰──────────────────────────────────────────────────────────╯
 Ready   alt+z expand · esc input · alt+h help        opus-5 · 23% ctx · $0.05
```

- **Tab bar:** the active tab is a Brand bold pill on Surface; the others are
  muted. State glyphs: `▶` Warning, `●` busy, `✓`, `✗`. Sub-agents get an
  Agent `↳` prefix. Overflow markers carry counts (`‹7`, `3›`) and turn
  Warning when a hidden tab awaits review.
- **Status bar** (`screen.StatusReporter`):
  - **Left:** the state: `Ready`, a spinner with the running tool's title (or
    `Running <Verb>`) and the elapsed time, `Cancelling…`, or Warning `▶
    Review · <title>`. Then `↓ N more` when scrolled up, then hints.
  - **Right:** facts by priority: `N% ctx` > model > cost (hidden at $0) >
    reasoning (when set) > role (when not the default).
  - **Toasts** (`AlertMsg{Level}`) replace the hints in place. Errors stay up
    for 6s.
- **Input:** rounded border, Brand when focused and Subtle when blurred. A
  `›` prompt. Height follows soft-wrapped rows.
- **Agent strip:** one quiet line between the timeline and the input, blank
  when there are no agents.
- **Chats tab:** list and preview separated by whitespace (no rule).
- **Exact layout:** every screen renders exactly its `SetSize` height and
  width.

## Timeline

- **Grid:** columns 0–1 are the gutter (`▌` on selection), columns 2–3 the
  marker (`›`, state glyph, `∴`, `@`), and text starts at col 4. Nested work
  steps sit 2 columns further in.
- **Spacing:** 1 blank line before each user turn (its band is padded), 1 between items, 0 between
  one-row items.
- **User turn:** a `ColorSurfaceTurn` band (the background lifted and tinted
  with Brand), padded by a band line above and below, with a bold Brand `›`
  and text ink. An agent-sent message gets a neutral `ColorSurface` band with
  an Agent `›`.
- **Band width:** backgrounds (turns, selection) stop at the text column
  (`bandWidth`: prose width plus padding), not the terminal edge.
- **Selection:** the selected lines on `ColorSurfaceStrong`, a Brand `▌` and a
  bold marker. In fence mode only the selected fence is painted. Turns and the
  review card keep their own background when selected; the `▌` marks them.
- **Bands own their background:** `band` rewrites every reset and every
  background the line sets itself (inline code, ANSI/256/truecolor) to the
  band's color, so nothing punches through.
- **Markdown:** glamour's **Dracula** theme (the user likes its colors and
  tables), with block margins and prefixes dropped so text sits on the grid.
  Don't replace it with a palette-derived theme. Prose wraps at
  `markdown.MaxProseWidth` (100); code and diffs use the full width. ```diff
  fences skip glamour.
- **Thought:** `∴ Thought · N lines`. System prompt, injected files,
  `◌ Queued`, `⊘ Turn cancelled` and `✗` errors are one line each.
- **Expanded bodies** are indented with spaces only. Results are capped at
  12 lines with `… N more lines · alt+o open`.

## Tool calls

- **Titles:** every builtin schema gets a required `tool_call_title` via
  `pbjson.WithTitle(tool.TitleDescription)`. Remote tool sets get it too,
  unless their config sets one (it's folded into the tool-set cache key).
  `tool.UnmarshalArguments` sets the title aside and parses everything else
  strictly. `tool.Title` reads it.
- **Row:** `glyph Title  Verb subject · outcome`, with the title in Text and
  the mechanics in Muted, kept close by. Failures (`Summary.Failed` or an
  error result) get Danger `✗` and outcome. Without a title, the row is
  `glyph Verb subject · outcome`.
  - `tool.Summarizer` supplies the mechanics only (Verb/Subject/Outcome/Failed).
  - Verbs are API (counts group by them): Read, View, Shell,
    Edit/Create/Delete/Rename, Search lores, Launch agents, Message, Check
    agents, Wait, Cancel, Discover, plus humanized RPC methods.
- **Finished turns fold** (`work.go`): once a turn has yielded
  (`timeline.SetLive(false)`), everything between the prompt and the trailing
  assistant text becomes one work row: `✓ Worked [dur] · N tool calls · Verb n
  … · k failed`. That covers calls, thoughts and intermediate notes.
  - The answer stays visible, and so do pending review cards.
  - The live turn never folds. Folding needs at least 2 steps.
  - The row id is `work-<first child id>`. Expanded, the steps nest and can
    each be selected and expanded.
- **Review card:** a Warning rounded border with the row riding in the top
  border, the display message, and the full request. The hints live in the
  status bar.

## Keys

- **Keep the existing bindings. Never bind alt+digits** (they conflict with
  the user's tab navigation; tab jumps stay on alt+F1..F9).
- **tab** toggles input ↔ timeline in every state. Entering the timeline
  selects the pending review card, else the last item.
- **esc** returns to the input. **pgup/pgdn** scroll the timeline from
  anywhere. **enter/alt+z** expand.
- **alt+p/alt+n** browse the shared, persisted input history from either
  focus.
- **Sub-agents:** `wait_operations` returns at the first finished operation
  by default (`ALL` must be asked for), so launchers act on each report as it
  lands.
- **alt+t** cycles reasoning effort: model default, low, medium, high, xhigh,
  max (`session.CycleReasoningEffort`).
- **ctrl+c: one press, one visible effect.** Close a modal, else cancel the
  turn (`Cancelling…`), else clear the input, else open the quit confirm.
  ctrl+w is the only key that closes a tab.
- **Destructive actions confirm** via `screen.ConfirmMsg`. In the help modal,
  esc closes, pgup/pgdn page, and any other key closes.

## Copy and the editor

- `cli/tui/clipboard.Write` copies natively when a display exists, and always
  via OSC 52 (works over SSH).
- **alt+o / alt+w / alt+shift+o** hand over plain markdown documents
  (`timeline/document.go`), never JSON blobs.
  - A tool call document holds the title, the tool name and outcome, the
    request (the tool's markdown, else plain `key: value`), and the full
    result.
  - Top-level multi-line strings (shell output, file contents) get their own
    fences, so newlines stay real.
  - Injected file groups are headed and fenced per file.

## Verifying

- **Build and render:** `plz build //docs/screenshots`, then from
  `docs/screenshots/` run `../../plz-out/bin/docs/screenshots/screenshots -out
  /tmp/f -text [-scene NAME] [-width W -height H] [-background #282a36]`.
- **Look at it:** `~/go/bin/freeze /tmp/f/X.ansi --output /tmp/f/X.png
  --theme dracula --padding 20 --font.size 13`, then view the PNG.
- **Scenes:**
  - Each area registers its own in `scenes_*.go`.
  - Each scene works on a fresh fixture copy.
  - Scene tool calls carry `tool_call_title`.
- **Sizes:** check 80×24, 120×36 and 160×45.
- `render.sh` regenerates the site SVGs.
