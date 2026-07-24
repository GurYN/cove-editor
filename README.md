# Cove

A terminal IDE you can use in five minutes: no tutorial, no muscle-memory tax.

Cove is a GUI-native terminal editor written in Go. If you come from VS Code, Zed, or JetBrains, everything works the way you expect: visible menus and tabs, a command palette, familiar shortcuts (`Ctrl+S` saves, `Ctrl+P` opens the palette), and first-class mouse support. The differentiator is **discoverability**, not a feature list.

![Cove demo](assets/cove-demo.gif)

## Features

- **Fast on big files**: rope buffer + virtualized viewport; keystroke-to-render under one frame on a 50k-line file (enforced by CI perf gates).
- **Tree-sitter syntax highlighting** for fourteen languages plus the config files around them (`.env`, `.gitignore`, `go.mod`, lockfiles, shell dotfiles, …), with structural selection (`Ctrl+E` expands the selection to the enclosing syntax node) and embedded-language support: `<script>`/`<style>` in HTML and fenced code blocks in Markdown highlight as the real thing.
- **LSP built in**: diagnostics, go-to-definition (`F12`), references (`Shift+F12`), hover docs (`Ctrl+K`), rename (`F2`), completion (`Ctrl+Space`), formatting, quick fixes and refactors (`Alt+Enter` — including server commands that create files, like gopls's *add test* and *extract to new file*), a symbol outline (`Ctrl+T`), project-wide symbol search (`F3`), and a problems list (`F8`). Go, Python, TypeScript/JavaScript, Rust, HTML, CSS, and Terraform work out of the box — including TypeScript 7's native language server (both the push and pull diagnostics models are supported).
- **Search across the project** (`F7`): .gitignore-aware, smart-case, sees unsaved buffer content, and every hit lands in a filterable picker. *Replace in Project…* (palette) previews the count and applies undoably to open files.
- **A jump list**: `Alt+Left` walks back through go-to-definition, symbol, and search jumps; `Alt+Right` walks forward. Navigation is never a one-way door.
- **Sessions restore themselves**: quit and reopen the same directory — tabs, cursors, the split, and sidebar width come back. Opening an explicit file skips it (`cove main.go` means exactly that).
- **Command palette** (`Ctrl+P`): every action is discoverable and shows its keybinding and rebindable ID.
- **File tree, tabs, fuzzy file finder** (`Ctrl+O`): the chrome you expect from a GUI editor. The tree shows git status at a glance — new, modified, and conflicted files are tinted, folders containing changes get a dot — and can create, rename, and delete files in place.
- **Split panes** (`Ctrl+\`): one vertical split with a draggable divider; both panes share the tab list, `F6`/`Shift+F6` cycles through panels.
- **Mouse support that actually works**: click to place the cursor, click tabs and tree entries, drag to select, drag the split divider and panel heights.
- **Integrated terminal** (`Ctrl+J`): your shell in a panel under the editor, with scrollback (mouse wheel or `Shift+PgUp`/`PgDn`), multiple instances (the `+` button), and a draggable height. Register your favorite TUI apps (`lazygit`, `redis-tui`, `btop`, …) in the config and they get their own palette entry and optional keybinding, running as a named panel instance — invoking again refocuses the running app instead of starting a second one.
- **Git built in** (`Ctrl+G`): a Zed-style panel with staged/unstaged files, per-file diffs in a read-only tab, commit, amend (keeps a multi-line message intact when you only add files), undo last commit (keeps changes staged), push/pull/fetch (a branch with no upstream is published automatically; a push rejected because you rebased or amended offers `--force-with-lease` behind a confirm), stash (everything, or just the selected file — pop brings it back), sync your branch (fetch + rebase onto any branch from a picker, carrying uncommitted work across), and per-file discard/restore. Multi-repo folders just work: open a directory containing several checkouts and the panel shows one section per repo, with every action targeting the repo under the cursor (or the active file's). Commit history opens in a fuzzy picker; Enter on any commit opens its full diff. Gutter signs mark added/modified/deleted lines as you type, inline blame (*Git: Toggle Inline Blame* in the palette) shows who last touched the cursor line, and the current branch and ahead/behind counts live in the status bar.
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

Syntax highlighting ships in the binary for Go (including `go.mod`), Python, TypeScript/JavaScript (including JSX/TSX), Rust, HTML (also `.svg`/`.xml`), CSS, Bash, JSON, TOML, YAML (including Docker Compose files), HCL/Terraform (`.tf`, `.tfvars`, `.hcl`), Dockerfile (`Dockerfile`, `Dockerfile.*`, `Containerfile`, `*.dockerfile`), and Markdown (also `.mdx`). HTML highlights embedded `<script>` and `<style>` blocks; Markdown highlights bold/italic/links/code spans and fenced code blocks in their own language (` ```go `, ` ```js `, …).

Everyday config files color too, reusing those grammars: `.env` (and `.env.*`), `.gitignore`/`.dockerignore`/`.gitattributes`, shell dotfiles (`.bashrc`, `.zshrc`, …), INI-style files (`.ini`, `.cfg`, `.properties`, `.editorconfig`, `.gitconfig`, `.npmrc`), lockfiles and manifests (`Cargo.lock`, `Pipfile`, `uv.lock`, `poetry.lock`, `Procfile`), and `Makefile`/`Justfile`.

Language intelligence needs the language's server on your `PATH`:

| Language   | Server                       | Install                                              |
| ---------- | ---------------------------- | ---------------------------------------------------- |
| Go         | `gopls`                      | `go install golang.org/x/tools/gopls@latest`          |
| Python     | `pyright-langserver`         | `npm i -g pyright`                                    |
| TypeScript / JavaScript | `tsc --lsp` (TypeScript 7+) | `npm i -g typescript`                       |
| Rust       | `rust-analyzer`              | `rustup component add rust-analyzer`                  |
| HTML / CSS | `vscode-html-language-server` / `vscode-css-language-server` | `npm i -g vscode-langservers-extracted` |
| Terraform  | `terraform-ls`               | `brew install hashicorp/tap/terraform-ls`             |

Still on TypeScript 5? Nothing to configure: Cove probes your `tsc` version once and falls back to `typescript-language-server` (`npm i -g typescript-language-server typescript@5`) when tsc predates the native server. A `[lsp.typescript]` entry in the config file overrides the probe entirely.

Any other language server speaking stdio registers in the config file:

```toml
[lsp.zig]
command = ["zls"]
extensions = ["zig"]
```

No server installed? Cove still works as a fast editor with syntax highlighting.

## Key bindings

Everything below is also in the command palette (`Ctrl+P`), which shows the current binding next to each action.

| Key             | Action                        |
| --------------- | ----------------------------- |
| `Ctrl+P`        | Command palette               |
| `Ctrl+O`        | Go to file (fuzzy finder)     |
| `Ctrl+S`        | Save                          |
| `Ctrl+F` / `Ctrl+R` | Find / find & replace     |
| `F7`            | Search in project             |
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
| `F3`            | Go to symbol in project       |
| `Alt+Enter`     | Quick fix / code action       |
| `F2`            | Rename symbol                 |
| `F8`            | Problems list                 |
| `Alt+Left` / `Alt+Right` | Jump back / forward  |
| `Ctrl+Q`        | Quit (asks for confirmation; warns about unsaved files) |

Every action has a stable ID (shown in the palette footer) and can be rebound in the config file. *File: Save All*, *Edit: Duplicate Line*, *Edit: Delete Line*, and more live in the palette without a default binding.

When the terminal panel has focus, every key goes to your shell except the ones that keep Cove reachable: `Ctrl+J` (hide panel), `Ctrl+P`/`F1` (palette), `Ctrl+B` (sidebar), `Ctrl+G` (git panel), `F6`/`Shift+F6` (cycle panels), `Ctrl+Q` (quit), and `Shift+PgUp`/`PgDn` (scrollback). Rebinding any of these moves the exception with it. (tmux inside Cove: rebind `sidebar.toggle` to free up `Ctrl+B` for the tmux prefix.)

> **Shift+Enter in the terminal panel** (Claude Code and other TUI apps): if your terminal is configured to send a newline (`\n`) for `Shift+Enter`, that byte is indistinguishable from `Ctrl+J` — so it toggles the panel instead of reaching your app. Configure it to send `\x1b\r` (ESC + CR) instead, which TUI apps read as the same "insert newline" chord. In Ghostty: `keybind = shift+enter=text:\x1b\r`. The same applies to any terminal with a custom `Shift+Enter` → `\n` mapping (iTerm2, WezTerm, Alacritty, …).

`Ctrl+B` and `Ctrl+G` are tri-state: they open and focus their panel, refocus it if it's open but unfocused, and close it when it already has focus.

In the file tree: `n` new file, `N` new folder, `r` rename, `x` delete (with confirm). Right-click any row for the same actions as a menu.

### Git panel

Inside the panel (all of this is also in the palette):

| Key       | Action                                    |
| --------- | ----------------------------------------- |
| `Space`   | Stage / unstage the selected file          |
| `Enter`   | Open the file's diff (read-only tab)       |
| `o`       | Open the file itself (on a conflict row: keep ours) |
| `c`       | Commit staged files                        |
| `m`       | Amend last commit (Enter keeps the message; typing rewords) |
| `z`       | Undo last commit (keeps changes staged, with confirm) |
| `l`       | Commit history (fuzzy picker; Enter opens the commit's diff) |
| `g`       | Commit graph (read-only tab; Enter on a line opens that commit's diff) |
| `b`       | Switch branch (fetches first; remote branches check out as tracking) |
| `s`       | Sync branch: fetch, then rebase onto a picked branch (uncommitted work rides along via autostash) |
| `h` / `p` | Stash the selected file / pop the latest stash |
| `a` / `u` | Stage all / unstage all                    |
| `o` / `t` | Resolve the selected conflict: keep ours / keep theirs (whole file) |
| `x`       | Discard the file's changes (with confirm)  |
| `f`       | Fetch                                      |
| `r`       | Refresh status                             |
| `Esc`     | Back to the editor                         |

Mouse: clicking a file's status letter toggles staging; clicking its name opens the diff. Right-click opens a context menu — file actions on a file row (stage, diff, open, stash, discard), resolve on a conflict row, repo actions (commit, push, pull…) anywhere else. Push, pull, fetch, *New Branch…*, *Stash All Changes*, and *Push — Force With Lease* are in the palette.

**Syncing a branch before the PR.** On a feature branch, `s` fetches and lists every other branch — pick `origin/main` and Cove rebases your branch onto it, autostashing uncommitted work across the rebase. If the branch was already pushed, the next push detects the rewritten history and offers a `--force-with-lease` push (never a blind force: it refuses if the remote gained commits you haven't seen). A conflicted rebase points you to the terminal to resolve and `git rebase --continue`.

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

[git]
view = "flat"                     # or "tree": changed files under collapsible directories (Enter/Space/click folds)

[keys]
"file.save" = "ctrl+shift+s"   # rebind any action by its ID

[lsp.go]
command = ["gopls"]            # override or add language servers

[colors]
"git.added" = "#98c379"        # override any theme color, incl. git states

[apps.lazygit]                 # favorite TUI apps: palette entry "App: lazygit",
command = ["lazygit"]          # runs as a named terminal-panel instance
key = "ctrl+alt+g"             # optional
```

Config mistakes don't fail silently: a binding that collides with an existing shortcut, a key terminals can't deliver (`ctrl+i` arrives as Tab), or an unknown action ID shows up in a toast at startup.

## Status

In active development, pre-1.0. The v1 scope is deliberately tight: editing, chrome, LSP for four languages, an integrated terminal, git integration (panel, staging, diffs, commit, amend, undo-commit, history & visual graph, push/pull with a safe force-with-lease path, branch sync via rebase, stash, remote-aware branch switching, in-editor conflict resolution, restore, gutter signs, inline blame, file-tree markers, multi-repo folders), split panes, project-wide search & replace, code actions, a jump list, per-workspace session restore, and user-defined TUI app launchers — all built and recently hardened by a full bug-hunt pass (UTF-8-safe cursor movement, LSP process lifecycle, tree-sitter memory management, non-ASCII git filenames). Plugins and debugging are deferred to v2.

## Contributing

```sh
go test ./...                      # full suite
go test ./internal/... -bench .    # benchmarks
```

The performance gates (`TestKeystrokeLatency50k`, `TestKeystrokeLatencyWithSyntax`) are hard limits (p99 keystroke→frame < 16ms) and run in CI.

## License

[MIT](LICENSE)
