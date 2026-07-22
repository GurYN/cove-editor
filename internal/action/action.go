// Package action is Cove's command registry — the keystone of the
// discoverability thesis (PRD §5.4). Every user action is a named entry
// here; the palette, the keybinding dispatcher, and (later) TOML rebinding
// and plugins all read this one data structure. An action that is not in
// the registry does not exist.
package action

import (
	"sort"

	tea "github.com/charmbracelet/bubbletea"
)

// Context scopes a binding to whichever pane has focus. Global actions fire
// anywhere in edit mode.
type Context string

const (
	Global  Context = "global"
	Editor  Context = "editor"
	Sidebar Context = "sidebar"
	Git     Context = "git" // the git panel
)

// Action is one named command. Key is the default binding in
// tea.KeyMsg.String() form ("ctrl+s", "shift+down", ...); empty means
// palette-only. Do returns a tea.Cmd (usually nil); the receiver is the
// app, passed as any to avoid an import cycle.
type Action struct {
	ID     string // "file.save"
	Title  string // "File: Save" — what the palette shows
	Key    string
	When   Context
	Hidden bool // bound but not palette-listed (movement keys)
	Do     func(app any) tea.Cmd
}

type Registry struct {
	list  []Action
	byKey map[string]int // "context\x00key" -> index
	byID  map[string]int
}

func NewRegistry() *Registry {
	return &Registry{byKey: map[string]int{}, byID: map[string]int{}}
}

func (r *Registry) Register(a Action) {
	r.byID[a.ID] = len(r.list)
	if a.Key != "" {
		r.byKey[string(a.When)+"\x00"+a.Key] = len(r.list)
	}
	r.list = append(r.list, a)
}

// Lookup resolves a key press for a focused context, falling back to
// Global. Returns nil if unbound.
func (r *Registry) Lookup(ctx Context, key string) *Action {
	if i, ok := r.byKey[string(ctx)+"\x00"+key]; ok {
		return &r.list[i]
	}
	if ctx != Global {
		if i, ok := r.byKey[string(Global)+"\x00"+key]; ok {
			return &r.list[i]
		}
	}
	return nil
}

// Rebind changes an action's key (empty = palette-only) and reindexes.
// Returns false for unknown IDs. On key conflict, the later-registered
// action wins.
func (r *Registry) Rebind(id, key string) bool {
	i, ok := r.byID[id]
	if !ok {
		return false
	}
	r.list[i].Key = key
	r.byKey = map[string]int{}
	for j, a := range r.list {
		if a.Key != "" {
			r.byKey[string(a.When)+"\x00"+a.Key] = j
		}
	}
	return true
}

// Owner returns the ID of a different action bound to key in a context
// that collides with when (same context, or Global on either side — Lookup
// falls back to Global, so those shadow each other). "" means no conflict.
func (r *Registry) Owner(id, key string, when Context) string {
	for _, a := range r.list {
		if a.ID != id && a.Key == key && (a.When == when || a.When == Global || when == Global) {
			return a.ID
		}
	}
	return ""
}

// ByID returns the action with the given ID, or nil.
func (r *Registry) ByID(id string) *Action {
	if i, ok := r.byID[id]; ok {
		return &r.list[i]
	}
	return nil
}

// Palette returns all palette-visible actions sorted by title, so the
// "Category:" prefixes group together instead of surfacing in whatever
// order newRegistry happens to declare them.
func (r *Registry) Palette() []Action {
	out := make([]Action, 0, len(r.list))
	for _, a := range r.list {
		if !a.Hidden {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}
