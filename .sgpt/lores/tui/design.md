---
title: TUI design standard
description: The sgpt TUI's visual and interaction doctrine — content over chrome, the semantic palette and glyph set, the screen layout, how messages and tool calls render (one-row calls, tight runs, folded runs, the review card), the key doctrine (keep bindings, never alt+digits, ctrl+c never destroys), and how to verify with the screenshot harness.
labels:
  topic: tui
---

# TUI design standard

## Principles

1. **Content over chrome.** No boxes around prose. Hierarchy comes from ink
   (Text > Muted > Subtle), indentation and whitespace, not borders. A border
   is a signal, so the one thing that gets a box is what needs you: the
   tool call under review.
2. **Hue means state or identity.** Brand (violet) is focus, selection and the
   user's voice. Warning (amber) is "waiting on you". Success, Danger, Agent,
   File and Info each mean one thing. Never use a color only to decorate.
3. **Quiet until it matters.** Resolved work folds down to one line. Live work
   (streaming, running, review) stays expanded and gets the eye.
4. **Every key is discoverable in context.** The status bar shows the 2–4
   hints that apply right now. alt+h shows everything. Hints come from the
   keymap, never from hand-written strings.
5. **Width-1 glyphs, no emoji in chrome.** Emoji widths vary between
   terminals and break alignment. Use the glyph set in `styles/palette.go`.
6. **Adaptive.** Colors come from `styles.Color*` (light and dark variants via
   `compat.AdaptiveColor`). Never write hex values outside `palette.go`.

## Palette and helpers (`cli/tui/styles/palette.go`)

- Inks: `ColorText`, `ColorMuted`, `ColorSubtle`. Raised background:
  `ColorSurface`.
- Hues: `ColorBrand`, `ColorInfo`, `ColorSuccess`, `ColorWarning`,
  `ColorDanger`, `ColorAgent`, `ColorFile`.
- Glyphs: `✓ ✗ ▶ ● ◌ ⊘ › │ ▌ ↳ … ·` (`Glyph*` constants).
- `styles.Hint(key, action)` and `styles.Hints(...)` render key hints.
- Numbers go through `cli/tui/format` (`Price`, `Tokens`, `Duration`, `Age`).
  Never write `fmt.Sprintf("$%.4f")` inline.
- The legacy color names in `styles.go` are deprecated aliases. Migrate off
  them.

## Layout

```
 Chats  Rename pendingReview  ▶ audit agent                    ← tab bar (1 line)

 ┃ rename pendingReview to awaitingVerdict                     ← user turn

   Renaming the type and the map that holds it.                ← assistant prose

   ✓ Read   internal/session/review.go             120 lines   ← tool run (tight)
   ✓ Shell  rg pendingReview                       14 matches
 ╭ ▶ Edit   internal/session/review.go ─────────────────────╮  ← review card
 │   @@ -4,7 +4,7 @@ …                                      │
 │   ctrl+j accept · alt+shift+r reject · alt+shift+a always │
 ╰───────────────────────────────────────────────────────────╯

 ╭───────────────────────────────────────────────────────────╮
 │ › Message sgpt…                                           │  ← input (brand border focused, subtle blurred)
 ╰───────────────────────────────────────────────────────────╯
 ● Thinking · 12s   ctrl+c cancel        opus-5 · high · 23% · $0.05   ← status bar
```

- **Top:** the tab bar only. No per-chat title bar. Model, reasoning, context
  and cost move to the right side of the status bar. The tool list lives in
  alt+i and the tool picker.
- **Bottom:** the input, then the status bar. Left side of the status bar:
  turn state (spinner, elapsed time) and contextual hints. Toasts (alerts)
  temporarily replace the hints, colored by level; errors stay up longer.
  Right side: compact session facts, dropped in priority order as width
  shrinks.

## Timeline

- A two-column left gutter on every item. While the timeline has focus, the
  selected item shows a brand `▌` on each of its lines. Layout never shifts
  on selection.
- One blank line between items, except between tool rows in a run.
- **User turn:** brand `┃` rule plus text-ink markdown. A message from another
  agent uses an Agent-colored rule with a `↳ sender` label.
- **Assistant prose:** plain markdown with no frame, aligned with tool rows.
- **Thought:** folds to `∴ Thought · N lines` (muted). Expanded, it's muted
  text behind a subtle rule.
- **System prompt, injected files, queued, cancelled, error:** a single line
  each (muted, File, muted, muted, Danger), expandable where there's content.

## Tool calls

- **One call is one row:** `glyph Verb Subject … Outcome`.
  - The glyph shows state: spinner while running or streaming, Warning `▶`
    for review, Success `✓`, Danger `✗`.
  - Verb is text ink. Subject is muted and truncated. Outcome is muted, or
    Danger for errors.
  - Tools supply this through `tool.Summarizer`
    (`Summary{Verb, Subject, Outcome}`, `Registry.Summarize`). The fallback is
    the tool name, the first short string argument, and the error text.
- **Runs:** consecutive calls render as a tight list. A resolved run longer
  than 3 calls folds into one row, `✓ 12 tool calls · Read 6 · Shell 4 ·
  Edit 2`, which enter or alt+z expands.
- **Expanded call:** the body is indented under the row behind a subtle `│`.
  - Request first. A tool's `RequestRenderer` (diff, command) wins. The
    default is `key: value` lines, never raw JSON braces.
  - Then the result, capped at a few lines with `… N more lines · alt+o
    open`.
- **Review card:** the only boxed element. A Warning rounded border holds the
  row, the display message, the full untruncated request, and an action hint
  line. It stays expanded until answered.
- **Display message** (`metadata.display_message`): a muted line under the
  row. Shown in full when expanded or in review, and appended truncated
  inline when folded.

## Keys

- **Keep the existing bindings.** They are muscle memory and fit around the
  user's window manager.
- **Never bind alt+digits.** They conflict with tab navigation in the user's
  environment. Tab jumps stay on alt+F1..F9.
- **Allowed additions:**
  - esc: back to the input, or close the overlay.
  - pgup/pgdn: scroll the timeline from anywhere.
  - enter (timeline focus): toggle expand on the selected item.
  - Mouse wheel: scroll.
- **ctrl+c never destroys.** In order of precedence:
  1. Cancel the running turn.
  2. Clear the input.
  3. Show the hint "ctrl+c again to quit"; a second press opens the quit
     confirm.

  Closing a tab is ctrl+w only.
- **Destructive actions confirm** via `screen.ConfirmMsg`.
- **Modals:** esc closes, they fit the screen (scroll or columns), and they
  are generated from the keymap.

## Verifying

`plz build //docs/screenshots`, then from `docs/screenshots/` run
`../../plz-out/bin/docs/screenshots/screenshots -out /tmp/frames -text
[-scene NAME] [-width 80 -height 24]`. This renders real frames against an
in-process fake AiService: `.ansi` with colors, `.txt` stripped. Each UI area
registers its scenes from its own `scenes_*.go` via `register(...)`. Check at
least 80×24 and 160×45. `docs/screenshots/render.sh` regenerates the site
SVGs (needs `freeze`).
