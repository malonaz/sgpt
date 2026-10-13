---
title: Sub-agents
description: Fan self-contained work out to chats of their own, and keep working while they run.
---

The `agent` tool group lets a chat launch **sub-agents**: one new chat tab
per task, each working from a fresh context. Sub-agents are asynchronous —
the launcher gets a handle back at once, keeps working, and hears from each
sub-agent when its turn ends. Either side can message the other at any time.

| Tool | Does | Review |
|---|---|---|
| `create_agents` | Launch a batch of sub-agents | manual |
| `message_agent` | Message a sub-agent you launched, or your launcher | auto |
| `get_operations` | Poll operations | auto |
| `wait_operations` | Block until operations finish | auto |
| `cancel_operations` | Stop operations | auto |

Enable them all with `--tool agent` (or `agent` in `@tool(...)` /
`chat.default_tools`). Arguments and results are in
[Built-in tools](/reference/builtin-tools/#agent-group).

## When the model uses it

The tool descriptions tell the model to delegate *self-contained* work: a
wide exploration of an unfamiliar area, a mechanical refactor across many
files, several investigations at once. Sub-agents share nothing with the
launching conversation, so everything they need travels in the request.

## Launching

| Argument | Meaning |
|---|---|
| `context` | A briefing written **once** and shared by every task — background, constraints, what to report |
| `tasks[]` | One sub-agent each: a `title` (the tab), a `query`, optional `files` and `model` |
| `files` / `tools` / `model` | Defaults for every task |

`create_agents` is a manual-review call: the request shows the briefing and
each task, the summary line the task count, granted tools, files and model.
Once approved it **returns immediately** with one operation per task; the
tabs open in the background and each sub-agent starts on its `query`.

The briefing becomes part of each sub-agent's system prompt, after the
role's prompt and a short **contract**: you were given a task by your
launcher, the final message of each turn is delivered to it verbatim as your
report, work to completion and state your assumptions. That is why a
sub-agent ends a turn with a self-contained answer rather than chatter.

## Operations

An **operation** (`operations/{n}`) is one sub-agent turn the launcher asked
for: the launch, or a later follow-up through `message_agent`. It resolves to
the sub-agent's final message for that turn. Each sub-agent has a stable
name (`agents/{n}`) — the handle to message it.

| State | Glyph | Meaning |
|---|---|---|
| `QUEUED` | ◌ | Waiting for a free sub-agent slot |
| `RUNNING` | ● | The sub-agent is working |
| `AWAITING_REVIEW` | ▶ | Blocked on your verdict for one of its tool calls |
| `SUCCEEDED` | ✓ | `response` holds the final message |
| `FAILED` | ✗ | No answer; `error` says why |
| `CANCELLED` | ⊘ | Stopped before it finished |

The `create_agents` and `message_agent` results in the timeline follow their
operations live, rendering each state and, once finished, the report.

## Getting results

- **Reports arrive as messages.** When an operation finishes, its result is
  delivered to the launcher as a message —
  `[operations/3 finished: report from sub-agent agents/2 "Explore auth"]`
  followed by the report — waking it if idle. A launcher can therefore end
  its turn after launching and pick the work back up when reports land.
- **`get_operations`** polls: the current state of named operations, or of
  every operation the chat started.
- **`wait_operations`** blocks until any (`RETURN_WHEN_ANY`, the default) or
  all (`RETURN_WHEN_ALL`) of the named operations — or every unfinished one
  — finish, or `timeout_seconds` elapse (default 600, at most an hour). The
  default lets a launcher act on each report as it lands (reply, follow up)
  and wait again, instead of idling until the slowest sub-agent finishes. A
  wait takes the results it collects, so those are not also delivered as
  messages.

A wait also **returns early when a message arrives** for the caller (reason
`MESSAGE`; the message follows the result). Without that, a launcher blocked
on a sub-agent and a sub-agent asking that launcher a question would wait on
each other forever.

## Talking back and forth

`message_agent` sends text to a sub-agent the chat launched (`agents/{n}`).
The message starts a new turn — or queues behind the running one — and the
call returns an operation resolving to the reply. Talking to a sub-agent is
within the grant approved at launch, so it auto-executes.

A sub-agent reaches its launcher with `agent: "parent"`, for what the
launcher needs while it keeps working. To block on an answer it simply ends
its turn with the question: that becomes its report, and the launcher's
follow-up arrives as its next message.

Messages between agents are user-role messages annotated
`sgpt.com/sender`. Their text opens with a bracketed line for the model —
`[message from your launcher]`, `[message from sub-agent agents/2 "X"]`, the
operation reports above — and the timeline renders them as their own bubbles
headed `↳ {sender}`.

## What a sub-agent inherits

Everything comes from its launcher, narrowed by the request and never
widened:

- **Tools** — the launcher's enabled tools, or the subset in `tools`, always
  with the `agent` group so it can talk back. A tool the launcher lacks is
  rejected before anything runs.
- **Model** — the launcher's, unless `model` (or a task's) says otherwise.
- **Permissions** — the launcher's [policy](/concepts/permissions/), live:
  when you "always accept" a tool in the launching chat, its sub-agents
  follow. A sub-agent cannot set its own rules.
- **Depth** — sub-agents may launch their own up to `chat.agent.max_depth`
  levels; beyond, `create_agents` is rejected but messaging still works.

## Cancellation

- `cancel_operations` stops named operations: a queued launch never starts,
  a running turn is cancelled. Finished ones are left untouched, and a
  cancellation the launcher asked for is not reported back.
- Stopping a chat's turn (<kbd>ctrl</kbd>+<kbd>c</kbd>) also stops its
  sub-agents' turns, recursively, without them reporting back — which would
  only wake the chat you just stopped. Stopping a sub-agent's turn in its own
  tab reports `[operations/{n} cancelled: the user stopped …]` to its
  launcher.
- Closing a tab retires that chat: its turn stops, pending replies fail and
  further messages to it are refused.

## Limits

| Setting | Default | Bounds |
|---|---|---|
| `chat.agent.max_concurrent` | 4 | Sub-agents running their **first** turn at once, per nesting level; the rest queue |
| `chat.agent.max_depth` | 2 | Nesting: 1 lets a chat launch sub-agents but not those launch their own |
| `chat.agent.max_agent_turns` | 25 | Turns a chat may start on messages from other agents before you speak in it again |

The last one bounds agents talking back and forth unattended: beyond it, an
agent message stays queued instead of starting a turn and an alert fires.
Your next message in that chat resets the count and carries the queued
messages to the model.

## What you see

Sub-agent tabs open **in the background**, right after their launcher's tab,
indented with `↳`. The tab bar marks a tab `▶` when it awaits review and `●`
while it works; `✓` or `✗` flags a finished or failed turn you have not
looked at yet.

Above the input, an **agent strip** lists the chat's launcher and its
sub-agents with their state glyphs and their total cost.

| Key | Action |
|---|---|
| <kbd>alt</kbd>+<kbd>u</kbd> | Jump to the launcher |
| <kbd>alt</kbd>+<kbd>g</kbd> | Jump to the next sub-agent — one awaiting review first |
| <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>w</kbd> | Close idle sub-agent tabs |

When a sub-agent needs your verdict, its call **surfaces in the root chat**
— the one you opened — as a pending call tagged `↳ {tab title}`, and you
answer it there exactly like the chat's own: <kbd>ctrl</kbd>+<kbd>j</kbd>
accepts, <kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>r</kbd> rejects,
<kbd>alt</kbd>+<kbd>shift</kbd>+<kbd>a</kbd> always accepts the tool for the
chat and every sub-agent under it. Calls are answered oldest first unless you
select one. Nested sub-agents surface in the same root. The sub-agent's tab
still marks `▶` and can answer its own calls; it is an ordinary chat, so you
can also intervene or keep talking to it.

Sub-agent chats are labelled `sgpt.com/category: agent` with
`sgpt.com/parent-chat` pointing at the launcher. The chat menu hides them
until toggled with <kbd>alt</kbd>+<kbd>s</kbd>. Quitting always asks for
confirmation, so running sub-agents are never dropped by accident.

Operations live in memory: they do not survive a restart. The chats do — reopen
one from the menu to read or continue it.
