# Cove

A terminal IDE you can use in five minutes — no tutorial, no muscle-memory tax.

Cove is a GUI-native terminal editor written in Go. If you come from VS Code, Zed, or JetBrains, everything works the way you expect: visible menus and tabs, a command palette, familiar shortcuts (`Ctrl+S` saves, `Ctrl+P` opens the palette), and first-class mouse support. The differentiator is **discoverability**, not a feature list.

## Features

- **Fast on big files** — rope buffer + virtualized viewport; keystroke-to-render under one frame on a 50k-line file (enforced by CI perf gates).
- **Tree-sitter syntax highlighting** with structural selection (`Ctrl+E` expands the selection to the enclosing syntax node).
- **LSP built in** — diagnostics, go-to-definition (`F12`), references (`Shift+F12`), hover docs (`Ctrl+K`), rename (`F2`), completion, formatting, and a problems list (`F8`). Go, Python, TypeScript, and Rust work out of the box.
- **Command palette** (`Ctrl+P`) — every action is discoverable and shows its keybinding and rebindable ID.
- **File tree, tabs, fuzzy file finder** (`Ctrl+O`) — the chrome you expect from a GUI editor.
- **Mouse support that actually works** — click to place the cursor, click tabs and tree entries, drag to select.
- **Multi-cursor editing, find & replace, undo/redo.**
- **No terminal traps** — `Ctrl+C` copies, `Ctrl+Z` undoes. An opt-in Vim keymap exists; it is never the default.

## Install

Download a binary from the [releases page](https://github.com/GurYN/cove-editor/releases), or build from source (Go 1.26+ and a C compiler required — tree-sitter uses CGo):

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

Syntax highlighting ships in the binary for Go, Python, TypeScript, Rust, and JSON. Language intelligence needs the language's server on your `PATH`:

| Language   | Server                       | Install                                              |
| ---------- | ---------------------------- | ---------------------------------------------------- |
| Go         | `gopls`                      | `go install golang.org/x/tools/gopls@latest`          |
| Python     | `pyright-langserver`         | `npm i -g pyright`                                    |
| TypeScript | `typescript-language-server` | `npm i -g typescript-language-server typescript`      |
| Rust       | `rust-analyzer`              | `rustup component add rust-analyzer`                  |

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
| `Ctrl+W`        | Close tab                     |
| `Ctrl+E`        | Expand selection to syntax node |
| `F12` / `Shift+F12` | Go to definition / references |
| `Ctrl+K`        | Hover documentation           |
| `F2`            | Rename symbol                 |
| `F8`            | Problems list                 |
| `Ctrl+Q`        | Quit                          |

Every action has a stable ID (shown in the palette footer) and can be rebound in the config file.

## Configuration

TOML at `~/.config/cove/config.toml` (or point `COVE_CONFIG` elsewhere). Open it from inside Cove via the palette: *Open Settings*.

```toml
theme = "cove-dark"   # or "cove-light"
keymap = "default"    # or "vim" (opt-in)

[keys]
"file.save" = "ctrl+shift+s"   # rebind any action by its ID

[lsp.go]
command = ["gopls"]            # override or add language servers
```

## Status

In active development, pre-1.0. The v1 scope is deliberately tight: editing, chrome, and LSP for four languages. Plugins, integrated terminal, git integration, debugging, and split panes are deferred to v2.

## Contributing

```sh
go test ./...                      # full suite
go test ./internal/... -bench .    # benchmarks
```

The performance gates (`TestKeystrokeLatency50k`, `TestKeystrokeLatencyWithSyntax`) are hard limits — p99 keystroke→frame < 16ms — and run in CI.

## License

[MIT](LICENSE)
