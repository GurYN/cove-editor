# Cove

A terminal IDE you can use in five minutes: no tutorial, no muscle-memory tax.

Cove is a GUI-native terminal editor written in Go. If you come from VS Code, Zed, or JetBrains, everything works the way you expect: visible menus and tabs, a command palette, familiar shortcuts (`Ctrl+S` saves, `Ctrl+P` opens the palette), and first-class mouse support. The differentiator is **discoverability**, not a feature list.

![Cove demo](assets/cove-demo.gif)

## Features

- **Fast on big files**: rope buffer + virtualized viewport; keystroke-to-render under one frame on a 50k-line file (enforced by CI perf gates).
- **Tree-sitter syntax highlighting** for twelve languages, with structural selection (`Ctrl+E` expands the selection to the enclosing syntax node) and embedded-language support: `<script>`/`<style>` in HTML and fenced code blocks in Markdown highlight as the real thing.
- **LSP built in**: diagnostics, go-to-definition (`F12`), references (`Shift+F12`), hover docs (`Ctrl+K`), rename (`F2`), completion (`Ctrl+Space`), formatting, a symbol outline (`Ctrl+T`), and a problems list (`F8`). Go, Python, TypeScript/JavaScript, Rust, HTML, and CSS work out of the box — including TypeScript 7's native language server (both the push and pull diagnostics models are supported).
- **Command palette** (`Ctrl+P`): every action is discoverable and shows its keybinding and rebindable ID.
- **File tree, tabs, fuzzy file finder** (`Ctrl+O`): the chrome you expect from a GUI editor. The tree shows git status at a glance — new, modified, and conflicted files are tinted, folders containing changes get a dot — and can create, rename, and delete files in place.
- **Split panes** (`Ctrl+\`): one vertical split with a draggable divider; both panes share the tab list, `F6`/`Shift+F6` cycles through panels.
- **Mouse support that actually works**: click to place the cursor, click tabs and tree entries, drag to select, drag the split divider and panel heights.
- **Integrated terminal** (`Ctrl+J`): your shell in a panel under the editor, with scrollback (mouse wheel or `Shift+PgUp`/`PgDn`), multiple instances (the `+` button), and a draggable height.
- **Git built in** (`Ctrl+G`): a Zed-style panel with staged/unstaged files, per-file diffs in a read-only tab, commit, undo last commit (keeps changes staged), push/pull/fetch (a branch with no upstream is published automatically), and per-file discard/restore. Multi-repo folders just work: open a directory containing several checkouts and the panel shows one section per repo, with every action targeting the repo under the cursor (or the active file's). Commit history opens in a fuzzy picker; Enter on any commit opens its full diff. Gutter signs mark added/modified/deleted lines as you type, inline blame (*Git: Toggle Inline Blame* in the palette) shows who last touched the cursor line, and the current branch and ahead/behind counts live in the status bar.
- **A commit graph you can actually read**: one row per commit, box-drawing lanes colored per branch, and each branch's name written vertically above its own column — with a line running down to its tip, even when that tip sits deep in history. Enter on a row opens the commit's diff.
- **Branch switching that knows your remote**: the picker (`b`) fetches first and lists remote branches alongside local ones — selecting `origin/foo` checks it out as a local tracking branch. Creating a branch whose name already exists on the remote checks that one out instead of forking an unrelated copy.
- **Merge conflicts resolved in the editor**: a conflicting pull highlights each `<<<<<<<` block — ours green, theirs blue — and the palette's *Merge: Accept Ours / Theirs / Both* resolves the block under the cursor as an undoable edit. Whole-file `o`/`t` shortcuts live in the panel's Conflicts section, a "Merging" banner tracks the in-progress merge, and `c` concludes it with git's prepared message prefilled.
- **Multi-cursor editing, find & replace, undo/redo** — plus the line-editing staples: toggle comment (`Ctrl+_`), move line up/down (`Alt+Shift+Up`/`Down`), duplicate/delete line, indent/outdent, select all occurrences, and go to line (`Ctrl+L`).
- **Plays nice with the outside world**: files edited outside Cove reload in place (undoable); a buffer with unsaved changes warns instead. The file tree and git status re-sync whenever the terminal regains focus, so changes from another shell or editor show up on their own.
- **No terminal traps**: `Ctrl+C` copies, `Ctrl+Z` undoes. An opt-in Vim keymap exists; it is never the default.

## Install

### Homebrew (macOS / Linux)

```sh
brew install GurYN/tap/cove
```

The formula builds from source on your machine (tree-sitter uses CGo, so there are no prebuilt bottles yet); Homebrew pulls in the Go toolchain as a build dependency automatically.

### Windows

Use [WSL](https://learn.microsoft.com/windows/wsl/install) and install with Homebrew (or a Linux release binary) inside it. There is no native Windows build — the integrated terminal relies on Unix PTYs.

### From a release or from source

Download a binary from the [releases page](https://github.com/GurYN/cove-editor/releases), or build from source (Go 1.26+ and a C compiler required; tree-sitter uses CGo):

```sh
git clone https://github.com/GurYN/cove-editor.git
cd cove-editor
go build -o cove ./cmd/cove
```

Then open a file or a directory:

```sh
cove main.go
cove .
```

## Language support

Syntax highlighting ships in the binary for Go, Python, TypeScript/JavaScript (including JSX/TSX), Rust, HTML, CSS, Bash, JSON, TOML, YAML (including Docker Compose files), Dockerfile (`Dockerfile`, `Dockerfile.*`, `Containerfile`, `*.dockerfile`), and Markdown. HTML highlights embedded `<script>` and `<style>` blocks; Markdown highlights bold/italic/links/code spans and fenced code blocks in their own language (` ```go `, ` ```js `, …).

Language intelligence needs the language's server on your `PATH`:

| Language   | Server                       | Install                                              |
| ---------- | ---------------------------- | ---------------------------------------------------- |
| Go         | `gopls`                      | `go install golang.org/x/tools/gopls@latest`          |
| Python     | `pyright-langserver`         | `npm i -g pyright`                                    |
| TypeScript / JavaScript | `tsc --lsp` (TypeScript 7+) | `npm i -g typescript`                       |
| Rust       | `rust-analyzer`              | `rustup component add rust-analyzer`                  |
| HTML / CSS | `vscode-html-language-server` / `vscode-css-language-server` | `npm i -g vscode-langservers-extracted` |

Still on TypeScript 5? Nothing to configure: Cove probes your `tsc` version once and falls back to `typescript-language-server` (`npm i -g typescript-language-server typescript@5`) when tsc predates the native server. A `[lsp.typescript]` entry in the config file overrides the probe entirely.

No server installed? Cove still works as a fast editor with syntax highlighting.

## Key bindings

Everything below is also in the command palette (`Ctrl+P`), which shows the current binding next to each action.

| Key             | Action                        |
| --------------- | ----------------------------- |
| `Ctrl+P`        | Command palette               |
| `Ctrl+O`        | Go to file (fuzzy finder)     |
| `Ctrl+S`        | Save                          |
| `Ctrl+F` / `Ctrl+R` | Find / find & replace     |
| `Ctrl+Z` / `Ctrl+Y` | Undo / redo               |
| `Ctrl+C` / `Ctrl+X` / `Ctrl+V` | Copy / cut / paste |
| `Ctrl+B`        | Toggle sidebar                |
| `Ctrl+G`        | Toggle git panel              |
| `Ctrl+J`        | Toggle terminal panel         |
| `Ctrl+W`        | Close tab                     |
| `Ctrl+PgUp` / `Ctrl+PgDn` | Previous / next tab |
| `Ctrl+\`        | Split pane                    |
| `F6` / `Shift+F6` | Next / previous panel       |
| `Ctrl+E`        | Expand selection to syntax node |
| `Ctrl+D`        | Add next occurrence to selection |
| `Alt+Up` / `Alt+Down` | Add cursor above / below |
| `Alt+Shift+Up` / `Alt+Shift+Down` | Move line up / down |
| `Ctrl+_`        | Toggle line comment           |
| `Ctrl+L`        | Go to line                    |
| `F12` / `Shift+F12` | Go to definition / references |
| `Ctrl+K`        | Hover documentation           |
| `Ctrl+Space`    | Trigger completion            |
| `Ctrl+T`        | Go to symbol (outline)        |
| `F2`            | Rename symbol                 |
| `F8`            | Problems list                 |
| `Ctrl+Q`        | Quit (asks for confirmation; warns about unsaved files) |

Every action has a stable ID (shown in the palette footer) and can be rebound in the config file. *File: Save All*, *Edit: Duplicate Line*, *Edit: Delete Line*, and more live in the palette without a default binding.

When the terminal panel has focus, every key goes to your shell except `Ctrl+J` (hide panel), `Ctrl+Q` (quit), and `Shift+PgUp`/`PgDn` (scrollback).

> **Shift+Enter in the terminal panel** (Claude Code and other TUI apps): if your terminal is configured to send a newline (`\n`) for `Shift+Enter`, that byte is indistinguishable from `Ctrl+J` — so it toggles the panel instead of reaching your app. Configure it to send `\x1b\r` (ESC + CR) instead, which TUI apps read as the same "insert newline" chord. In Ghostty: `keybind = shift+enter=text:\x1b\r`. The same applies to any terminal with a custom `Shift+Enter` → `\n` mapping (iTerm2, WezTerm, Alacritty, …).

`Ctrl+B` and `Ctrl+G` are tri-state: they open and focus their panel, refocus it if it's open but unfocused, and close it when it already has focus.

In the file tree: `n` new file, `N` new folder, `r` rename, `x` delete (with confirm).

### Git panel

Inside the panel (all of this is also in the palette):

| Key       | Action                                    |
| --------- | ----------------------------------------- |
| `Space`   | Stage / unstage the selected file          |
| `Enter`   | Open the file's diff (read-only tab)       |
| `c`       | Commit staged files                        |
| `z`       | Undo last commit (keeps changes staged, with confirm) |
| `l`       | Commit history (fuzzy picker; Enter opens the commit's diff) |
| `g`       | Commit graph (read-only tab; Enter on a line opens that commit's diff) |
| `b`       | Switch branch (fetches first; remote branches check out as tracking) |
| `a` / `u` | Stage all / unstage all                    |
| `o` / `t` | Resolve the selected conflict: keep ours / keep theirs (whole file) |
| `x`       | Discard the file's changes (with confirm)  |
| `f`       | Fetch                                      |
| `r`       | Refresh status                             |
| `Esc`     | Back to the editor                         |

Mouse: clicking a file's status letter toggles staging; clicking its name opens the diff. Push, pull, fetch, and *New Branch…* are in the palette.

With several repos in the opened folder, each renders as its own bold `name · branch` section and every panel action applies to the section under the cursor — the commit prompt names its target (*Commit to sources (main):*), and toasts are prefixed with the repo name. Actions run from the palette follow the active file's repo; for a file outside every repo, a one-shot picker asks. The status bar shows the current repo as `⎇ name:branch`.

Outside the panel, gutter signs next to the line numbers mark added, modified, and deleted lines as you type. *Git: Toggle Inline Blame* (palette) shows the last commit for the cursor line in the status bar: author, age, and summary; lines you have edited show *uncommitted changes*.

**Merge conflicts.** A pull that conflicts stops mid-merge (Cove pulls with merge semantics when you haven't configured `pull.rebase`): conflicted files appear in a *Conflicts* section, the status bar shows `(merging)`, and Enter on a conflicted file opens it at the first block with both sides highlighted (ours green, theirs blue — `merge.ours`/`merge.theirs` in `[colors]` to retheme). Resolve per block from the palette — *Merge: Accept Ours / Theirs / Both*, *Merge: Next Conflict* — or edit the markers by hand; every accept is a normal undoable edit. Stage the file (`Space`) when done, then `c` commits and concludes the merge (the message is prefilled from git). `o`/`t` in the panel take one side for the whole file.

## Configuration

TOML at `~/.config/cove/config.toml` (or point `COVE_CONFIG` elsewhere). Open it from inside Cove via the palette: *Open Settings*.

```toml
theme = "cove-dark"   # or "cove-light"
keymap = "default"    # or "vim" (opt-in)

[editor]
confirm_quit = true   # false: Ctrl+Q quits without asking

[files]
hidden = [".DS_Store", "*.pyc"]   # hide from the file tree (git panel still shows them)

[keys]
"file.save" = "ctrl+shift+s"   # rebind any action by its ID

[lsp.go]
command = ["gopls"]            # override or add language servers

[colors]
"git.added" = "#98c379"        # override any theme color, incl. git states
```

## Status

In active development, pre-1.0. The v1 scope is deliberately tight: editing, chrome, LSP for four languages, an integrated terminal, git integration (panel, staging, diffs, commit, undo-commit, history & visual graph, push/pull, remote-aware branch switching, in-editor conflict resolution, restore, gutter signs, inline blame, file-tree markers, multi-repo folders), and split panes — all built and recently hardened by a full bug-hunt pass (UTF-8-safe cursor movement, LSP process lifecycle, tree-sitter memory management, non-ASCII git filenames). Plugins and debugging are deferred to v2.

## Contributing

```sh
go test ./...                      # full suite
go test ./internal/... -bench .    # benchmarks
```

The performance gates (`TestKeystrokeLatency50k`, `TestKeystrokeLatencyWithSyntax`) are hard limits (p99 keystroke→frame < 16ms) and run in CI.

## License

[MIT](LICENSE)
