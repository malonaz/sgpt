package main

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// runtime is a minimal headless Bubble Tea loop: it executes commands on
// goroutines and feeds their messages back through Update, so the model
// behaves exactly as under a real program — just without a terminal.
type runtime struct {
	mu    sync.Mutex
	model tea.Model
	msgs  chan tea.Msg
	done  chan struct{}
}

func newRuntime(model tea.Model) *runtime {
	return &runtime{model: model, msgs: make(chan tea.Msg, 256), done: make(chan struct{})}
}

func (r *runtime) start() {
	r.exec(r.model.Init())
	go func() {
		for {
			select {
			case msg := <-r.msgs:
				r.handle(msg)
			case <-r.done:
				return
			}
		}
	}()
}

func (r *runtime) stop() { close(r.done) }

func (r *runtime) handle(msg tea.Msg) {
	switch m := msg.(type) {
	case nil:
		return
	case tea.BatchMsg:
		for _, cmd := range m {
			r.exec(cmd)
		}
		return
	case tea.QuitMsg:
		return
	}
	r.mu.Lock()
	model, cmd := r.model.Update(msg)
	r.model = model
	r.mu.Unlock()
	r.exec(cmd)
}

func (r *runtime) exec(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if msg := cmd(); msg != nil {
			r.msgs <- msg
		}
	}()
}

// send injects a message as the terminal would.
func (r *runtime) send(msg tea.Msg) { r.msgs <- msg }

// key sends a key press, e.g. key('m', tea.ModAlt).
func (r *runtime) key(code rune, mod tea.KeyMod) {
	r.send(tea.KeyPressMsg{Code: code, Mod: mod, Text: keyText(code, mod)})
}

func keyText(code rune, mod tea.KeyMod) string {
	if mod != 0 || code < ' ' {
		return ""
	}
	return string(code)
}

// typeText feeds text as individual key presses.
func (r *runtime) typeText(text string) {
	for _, code := range text {
		r.key(code, 0)
	}
}

// settle lets in-flight commands and messages drain.
func (r *runtime) settle(d time.Duration) { time.Sleep(d) }

func (r *runtime) view() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.model.View().Content
}
