---
title: Sub-agent persistence and restore
description: What a sub-agent chat records (parent-chat label for the server-side filter; sgpt.v1.AgentMetadata as protojson in the sgpt.com/agent-metadata annotation), why agent and operation names are random IDs, how a reopened chat restores its tree as dormant stand-ins (one ListChildChats per level), how a dormant agent wakes under its launcher, and how recorded operation snapshots resolve after a restart.
labels:
  topic: agent
---

# Sub-agent persistence and restore

The live sub-agent tree is `agent.Manager`'s, in memory. A reopened chat
(menu or CLI resume) rebuilds it from storage: `Manager.Restore`, called by
the TUI for every user-opened persisted chat tab.

## What a sub-agent chat records

Set by the session factory in `cli/chat/cmd.go` at launch:

- `sgpt.com/parent-chat` label: the launcher's chat ID — the only thing
  filtered on (`store.ListChildChats`, OR of IDs, oldest first), hence a
  label.
- `sgpt.com/agent-metadata` annotation: `sgpt.v1.AgentMetadata`
  (`sgpt/v1/agent.proto`) as protojson, via `store.AgentMetadata` /
  `store.SetAgentMetadata` (unknown fields dropped; absent or malformed
  reads as nil). It holds the name in the launcher's transcript
  (`agents/{8 hex}`), the approved tool grant and the reasoning effort —
  re-granted on reopen, the grant capped by the launcher's current tools —
  and `last_turn_outcome`, stamped by the session in `turn.complete` on
  chats carrying metadata only and persisted by the turn's closing flush (no
  extra RPC): a dormant agent's last known state.
- Chats predating the metadata restore as `agents/{chat ID}` (a name no
  transcript holds), state unknown, and reopen with what the launcher hands
  down.

## Names are random, not counters

`agents/…` and `operations/…` names are random IDs (`newName`): counters
restart at 1 every run, so an old transcript's `operations/1` would resolve
to an unrelated live operation.

## Dormant agents

- Restore lists one level per call, down to `max_depth`, and registers each
  child with a `dormantAgent` stand-in as its handle (a `tool.Agent`): the
  panel, `Children`/`Parent` and tab focus work unchanged. Nothing loads.
- A name already known in this run is re-parented to the reopened session
  instead of registered twice.
- Delivering to a stand-in (message_agent, a report, "parent") or
  `Manager.Wake` (panel enter, focus, menu open of its chat) loads it via
  `Launcher.LaunchAgent` with `LaunchRequest.Chat` set: same factory, history
  loaded and repaired, background tab. It wakes under its live launcher's
  `Caller()` — a dormant launcher wakes first. `bindLocked` moves children
  from the stand-in to the session; stale stand-ins forward to it.

## Recorded snapshots after a restart

`liveSnapshotLocked`: a live operation renders live; a recorded final state
stands (it can't change); a stale queued/running one takes its agent's last
known state, or unknown (`?`) when the agent isn't known.
