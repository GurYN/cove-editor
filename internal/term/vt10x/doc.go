/*
Vendored from github.com/hinshun/vt10x (MIT, see LICENSE) with one Cove
patch: primary-screen scrollback (history field in State, capture in
scrollUp, HistoryLen/HistoryCell accessors, View interface additions).
Keep other diffs from upstream at zero.

Package terminal is a vt10x terminal emulation backend, influenced
largely by st, rxvt, xterm, and iTerm as reference. Use it for terminal
muxing, a terminal emulation frontend, or wherever else you need
terminal emulation.

In development, but very usable.
*/
package vt10x
