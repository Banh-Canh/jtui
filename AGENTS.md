# AGENTS.md

## Project Overview

JTUI (Jellyfin Terminal User Interface) is a Go TUI application for browsing and interacting with a Jellyfin media server from the terminal. It is built on the Bubble Tea framework with Lip Gloss styling. Key features include library browsing, Quick Connect authentication, mpv-based media playback with IPC control, video downloading/offline mode, in-terminal thumbnail rendering, and vim-style navigation.

- **Module**: `github.com/Banh-Canh/jtui`
- **Language**: Go 1.24+
- **License**: MIT

## Repository Structure

```
jtui/
├── main.go                  # Entry point - calls cmd.Execute()
├── cmd/                     # CLI commands (Cobra)
│   ├── root.go              # Root command, config init, --version flag
│   └── browse.go            # "browse" subcommand
├── internal/                # Application-private code
│   ├── config/
│   │   └── config.go        # Configuration management (Viper, XDG paths)
│   ├── ui/
│   │   └── menu.go          # TUI model/update/view (Bubble Tea) - core UI logic
│   └── utils/
│       └── zap.go           # Logger initialization (Zap, structured JSON)
├── pkg/                     # Reusable Jellyfin API client library
│   └── jellyfin/
│       ├── client.go        # Client struct, HTTP setup, API module composition
│       ├── types.go         # Core types: Item interface, SimpleItem, DetailedItem
│       ├── builder.go       # ClientBuilder pattern, ConnectFromConfig factory
│       ├── auth.go          # Quick Connect auth, session persistence
│       ├── libraries.go     # Library browsing API
│       ├── items.go         # Items API (browse, details, resume, next up)
│       ├── playback.go      # Playback reporting, stream URLs, watched status
│       ├── search.go        # Search API with options pattern
│       └── download.go      # Download management, offline content discovery
├── .github/
│   └── workflows/           # CI/CD (build, test, lint, release)
├── config.yaml              # Example configuration
└── .goreleaser.yaml         # Cross-compilation config
```

## Architecture

The project follows the standard Go layout (`cmd/`, `internal/`, `pkg/`) with a clean layered architecture:

1. **CLI layer** (`cmd/`): Cobra-based command definitions. The root command launches the TUI directly; the `browse` subcommand pre-authenticates a Jellyfin client first.
2. **TUI layer** (`internal/ui/`): Implements the Bubble Tea Elm Architecture (Model-View-Update). The `model` struct in `menu.go` holds all UI state, with `ViewType` enum for navigation (LibraryView, FolderView, ItemView, SearchView). Async operations use Bubble Tea message types (`Cmd`/`Msg`).
3. **API client layer** (`pkg/jellyfin/`): Modular client with six sub-APIs (`Auth`, `Libraries`, `Items`, `Playback`, `Search`, `Download`), each as a separate struct with a `*Client` reference. Built via `ClientBuilder` pattern.
4. **Utilities** (`internal/`): XDG-compliant config paths and structured JSON logging.

**Data flow**: `main.go` -> `cmd/root.go` -> `internal/ui/menu.go` -> `pkg/jellyfin/*`

## Build and Run

```bash
# Build
go build -o jtui

# Run
./jtui          # launches TUI (authenticates internally)
./jtui browse   # pre-authenticates, then launches TUI

# Run tests
go test ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...
```

**Requirements**: Go 1.24+, `mpv` for playback, a Jellyfin server.

## Code Style and Conventions

### Formatting

- **Formatter**: `gofumpt` (stricter gofmt) -- enforced in CI
- **Max line length**: 140 characters -- enforced by `golines`
- Run locally before committing:
  ```bash
  gofumpt -w .
  golines --max-len=140 -w .
  ```

### Naming

- Standard Go conventions: `camelCase` unexported, `PascalCase` exported
- Packages: lowercase single words (`jellyfin`, `ui`, `cmd`, `config`, `utils`)
- Files: lowercase, descriptive (`client.go`, `types.go`, `auth.go`)
- Struct receivers: single letter matching type (`(c *Client)`, `(a *AuthAPI)`, `(m model)`)

### Error Handling

- Wrap errors with `fmt.Errorf()` using `%w` for wrapping or `%v` for context
- Error messages start lowercase and describe what failed
- In the TUI layer, async errors are propagated via Bubble Tea `errMsg` message types

### Comments

- Copyright headers on `cmd/` files: `/* Copyright (C) 2024 Victor Hang */`
- Doc comments on all exported functions and types
- Package comment on `pkg/jellyfin/client.go`

### Commit Messages

Follow the emoji convention used in the project:
- `feat ✨:` new features
- `fix 🐛:` bug fixes
- `chore 🧹:` maintenance
- `docs 📚:` documentation

## CI/CD

Four GitHub Actions workflows:

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `build.yaml` | Pull requests | GoReleaser snapshot (dry-run build) |
| `unittest.yaml` | Push/PR to `main` | `go test -coverprofile=coverage.out ./...` |
| `linting.yaml` | Pull requests | `gofumpt` + `golines --max-len=140` checks |
| `release.yaml` | Tag push | GoReleaser full release to GitHub Releases |

**Dependabot** is configured for weekly updates on both `gomod` and `github-actions` ecosystems.

## Testing

- Tests use the standard Go `testing` package
- CI runs `go test -coverprofile=coverage.out ./...` on push/PR to `main`
- No test files exist yet -- this is an area for contribution

## Key Types and Abstractions

### Core Interface

```go
// Item is the common interface for all Jellyfin items
type Item interface {
    GetName() string
    GetID() string
    GetIsFolder() bool
}
```

### API Client Modules

The `Client` struct composes six sub-API modules:

| Module | Type | Responsibilities |
|--------|------|-----------------|
| `Auth` | `AuthAPI` | Quick Connect auth, session load/save/validate |
| `Libraries` | `LibrariesAPI` | Get libraries, folders |
| `Items` | `ItemsAPI` | Browse items, details, resume, next up, recently added |
| `Playback` | `PlaybackAPI` | Report start/stop/progress, stream URLs, mark watched |
| `Search` | `SearchAPI` | Search items with configurable options |
| `Download` | `DownloadAPI` | Download/remove videos, offline content discovery |

### TUI State

The Bubble Tea `model` struct in `internal/ui/menu.go` holds all UI state including current view, item lists, cursor position, navigation path, details panel, playback state, and thumbnail cache. View transitions are managed through the `ViewType` enum.

## File Paths and Configuration

| Path | Purpose |
|------|---------|
| `~/.config/jtui/config.yaml` | User configuration |
| `~/.config/jtui/jtui.log` | Application logs (structured JSON) |
| `~/.cache/jtui/session.txt` | Persisted auth session |
| `~/.config/jtui/downloads/` | Downloaded media files |
| `/tmp/jtui-mpvsocket` | mpv IPC socket |
| `/tmp/jtui_kitty_*.jpg`, `/tmp/jtui_yazi_*.jpg` | Thumbnail cache |
