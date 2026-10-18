# Setting up Nimbo

One command installs what Nimbo needs, builds it and checks your computer.
You don't need to know Go, Rust or Docker to use it: the script explains every
step, asks before it installs anything, and the app's first-run guide takes care
of Docker afterwards.

| Your computer | Run (from the cloned folder) |
|---|---|
| macOS | `./setup.sh` |
| Linux (Ubuntu/Debian, Fedora, Arch, openSUSE) | `./setup.sh` |
| Windows 10/11 | `powershell -ExecutionPolicy Bypass -File .\setup.ps1` |
| Windows with WSL 2 | either `setup.ps1` in Windows, or `./setup.sh` inside WSL (needs WSLg for the window) |

## Commands

Both scripts take the same commands and options.

| `setup.sh` | `setup.ps1` | What it does |
|---|---|---|
| `./setup.sh` or `./setup.sh install` | `.\setup.ps1` | Check/install prerequisites, install dependencies, build the backend and the desktop app, offer to install the app and the `nimbo` CLI, run `nimbo doctor` |
| `./setup.sh dev` | `.\setup.ps1 dev` | Check prerequisites, install dependencies, build the backend, start the app in development mode (hot reload) |
| `./setup.sh build` | `.\setup.ps1 build` | Install dependencies and build everything; assumes prerequisites are installed |
| `./setup.sh doctor [--json]` | `.\setup.ps1 doctor [-Json]` | Build the backend if needed, then run `nimbo doctor` (exit code 0 = ready, 1 = something is missing) |
| `./setup.sh help` | `.\setup.ps1 help` | Usage |

| Option | `setup.sh` | `setup.ps1` |
|---|---|---|
| Answer "yes" to every question (unattended) | `--yes` / `-y` | `-Yes` |
| Never install or change anything outside the repository: missing prerequisites are only reported, and the offers to install the app and link the CLI are skipped | `--no-install` / `-n` | `-NoInstall` |

Without a terminal to ask (e.g. CI) and without `--yes`, every question is
answered "no". Re-running is safe: finished steps are fast no-ops.

## What `install` does

1. **Detects your computer** — OS, version, CPU (arm64/x86_64) and, on Linux,
   the package manager.
2. **Checks prerequisites** and shows ✓/✗ with versions:

   | Prerequisite | Why | Installed with |
   |---|---|---|
   | Xcode Command Line Tools (macOS) | Apple's compilers | `xcode-select --install` (Apple's installer window; the script waits for it) |
   | Homebrew (macOS) | package manager used for Go | official installer from [brew.sh](https://brew.sh) |
   | Tauri system libraries (Linux) | WebKitGTK window, tray icon, SVG, OpenSSL, compilers | your package manager, see [Linux packages](#linux-packages) |
   | C++ Build Tools + WebView2 (Windows) | Microsoft compilers and the Edge engine the window uses | `winget` (Visual Studio 2022 Build Tools, "Desktop development with C++"; Microsoft Edge WebView2 Runtime) |
   | Go 1.26+ | compiles the Nimbo backend | Homebrew (macOS), official tarball into `/usr/local/go` (Linux), `winget install GoLang.Go` (Windows) |
   | Bun 1.4+ | JavaScript runtime that builds the UI | official installer from [bun.sh](https://bun.sh) (`winget install Oven-sh.Bun` on Windows) |
   | Rust (minimum from `apps/desktop/src-tauri/Cargo.toml`) | compiles the desktop app shell | official [rustup](https://rustup.rs) installer (`winget install Rustlang.Rustup` on Windows) |
   | WSL 2 (Windows, reported only) | Docker Desktop runs on it | not installed by the script — the app guide explains `wsl --install` |
   | Docker (reported only) | runs the local cloud sandboxes | not installed by the script — **not needed to build**; the app's first-run guide helps you install and start it |

   For each missing item the script prints the exact commands and asks
   `Install X now? [Y/n]`. Commands that need administrator rights are always
   shown with `sudo` before they run; nothing is elevated silently. Tools
   installed in this run are put on `PATH` immediately (`~/.bun/bin`,
   `~/.cargo/env`, `/usr/local/go/bin`, `brew shellenv`); Homebrew and the Linux
   Go install also add a line to your shell profile so new terminals find them.
3. **Installs JavaScript dependencies** — `bun install --frozen-lockfile` in
   `apps/desktop` (falls back to `bun install` if the lockfile is out of date).
4. **Builds the backend** — `backend/bin/nimbo`, version stamped from
   `git describe` (or `<app version>-dev` without tags/commits).
5. **Builds the sidecar** — `bun run sidecar`, the same backend bundled inside
   the app.
6. **Builds the desktop app** — `bun run tauri build`; the script prints the
   produced bundles from `apps/desktop/src-tauri/target/release/bundle/`:
   `macos/Nimbo.app` and `dmg/*.dmg` on macOS, `deb/*.deb`, `rpm/*.rpm` and
   `appimage/*.AppImage` on Linux, `nsis/*-setup.exe` and `msi/*.msi` on Windows.
7. **Installs** (asks first):
   - macOS: copies `Nimbo.app` to `/Applications` (or `~/Applications` if
     `/Applications` isn't writable).
   - Linux: prints how to install the `.deb`/`.rpm` or run the AppImage.
   - Windows: runs the Nimbo installer (`*-setup.exe`).
   - The `nimbo` command for AI agents ([MCP](MCP.md)): links
     `~/.local/bin/nimbo` to the installed app's bundled binary
     (`/Applications/Nimbo.app/Contents/MacOS/nimbo`) or to `backend/bin/nimbo`,
     and warns if `~/.local/bin` is not on your `PATH`. On Windows it offers to
     add the folder containing `nimbo.exe` to your user `PATH`.
8. **Checks Docker and sandbox readiness** — runs `nimbo doctor` and prints the
   next steps.

## After setup: Docker and the first-run guide

Nimbo runs your infrastructure in local Docker containers, so it needs a Docker
engine. You don't need it to build, and you don't need to set it up by hand:
when you open the app, the first-run guide checks every requirement, marks what
is already fine, and for the rest explains in plain language what it is and
offers a one-click fix (start Docker, download the sandbox images) or a link.

`nimbo doctor` (or `./setup.sh doctor`) runs the same checks in a terminal:

```text
$ ./setup.sh doctor
```

It exits with `0` when everything required is ready and `1` otherwise; add
`--json` for machine-readable output. It reads the same settings as the app
(`~/.nimbo`, override with `NIMBO_HOME`).

Typical Docker options:

| OS | Engine |
|---|---|
| macOS | [Docker Desktop](https://www.docker.com/products/docker-desktop/) (Apple silicon or Intel build), [OrbStack](https://orbstack.dev) (`brew install --cask orbstack`), Colima or Rancher Desktop |
| Windows | [Docker Desktop](https://www.docker.com/products/docker-desktop/) (`winget install -e --id Docker.DockerDesktop`), which needs WSL 2 (`wsl --install` in an admin terminal, then restart) |
| Linux | [Docker Engine](https://docs.docker.com/engine/install/) for your distribution, then `sudo systemctl enable --now docker` and `sudo usermod -aG docker "$USER"` (log out and back in) |

## Linux packages

`setup.sh` installs the [Tauri prerequisites](https://v2.tauri.app/start/prerequisites/#linux)
for your package manager:

| Package manager | Packages |
|---|---|
| apt (Ubuntu, Debian, Mint, Pop!_OS) | `libwebkit2gtk-4.1-dev build-essential curl wget file libxdo-dev libssl-dev libayatana-appindicator3-dev` (or `libappindicator3-dev`) `librsvg2-dev` |
| dnf (Fedora) | `webkit2gtk4.1-devel openssl-devel curl wget file libappindicator-gtk3-devel librsvg2-devel libxdo-devel` and the `c-development` group |
| pacman (Arch, Manjaro) | `webkit2gtk-4.1 base-devel curl wget file openssl appmenu-gtk-module libappindicator-gtk3 librsvg xdotool` |
| zypper (openSUSE) | `webkit2gtk3-devel libopenssl-devel curl wget file libappindicator3-1 librsvg-devel` and the `devel_basis` pattern |

Other distributions: install the equivalents from the Tauri page above, then
run `./setup.sh` again.

## Windows notes

- Run `setup.ps1` from a normal (not administrator) PowerShell window; `winget`
  asks for administrator rights itself when an installer needs them.
- Script execution is blocked by default; `-ExecutionPolicy Bypass` allows it
  for this one run without changing your system policy.
- `winget` comes with "App Installer" from the Microsoft Store
  (<https://aka.ms/getwinget>).
- The Visual Studio Build Tools download is large (several GB) and can take a
  while; the script passes `--wait --passive --add Microsoft.VisualStudio.Workload.VCTools --includeRecommended`
  (plus the ARM64 tools on ARM devices).
- MSI bundles need the VBSCRIPT optional Windows feature (on by default). If
  the build fails with `failed to run light.exe`, enable it in *Settings → Apps
  → Optional features → More Windows features*.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `Bun 1.4+ ✗ found 1.x at …/.bun/bin/bun (first on PATH)` and "A newer Bun … is installed at …" | Two Buns are installed and the old one wins. Put the newer one's folder first in `PATH` (the script prints the line), or upgrade/remove the old one (`bun upgrade` for `~/.bun`, `brew upgrade bun` for Homebrew). The script never switches silently. |
| `Go 1.26+ … (auto toolchain; installed go1.25)` | Fine: Go ≥ 1.21 downloads the version `backend/go.mod` asks for (`GOTOOLCHAIN=auto`). If `GOTOOLCHAIN=local` is set, install Go 1.26+ instead. |
| `Missing prerequisites` with `--no-install` | Expected: `--no-install` only reports. Run `./setup.sh` and answer "yes", or install the listed items yourself. |
| `The lockfile is out of date` | Someone changed `apps/desktop/package.json` without updating `bun.lock`; the script installs without `--frozen-lockfile`. Commit the updated `bun.lock`. |
| Tauri build: `webkit2gtk-4.1` / `javascriptcoregtk` not found (Linux) | The system libraries are missing; run `./setup.sh` (not `build`) or install the [Linux packages](#linux-packages). |
| Tauri build fails while creating the `.dmg` (macOS) | The `.app` is still built in `target/release/bundle/macos/`. DMG creation scripts Finder; run the script from a logged-in desktop session (not over SSH) and allow Terminal to control Finder if macOS asks. |
| The app says Docker is not installed although `docker` works in Terminal (macOS) | Apps opened from Finder don't get your shell's `PATH`; Nimbo adds the usual Docker locations (Docker Desktop, OrbStack, Rancher Desktop, Colima, Homebrew) itself. If your engine lives elsewhere, set *Settings → Docker command* to its full path. |
| `permission denied … /var/run/docker.sock` (Linux) | `sudo usermod -aG docker "$USER"`, then log out and back in. |
| Docker CLI found but the engine is not running | macOS/Windows: open Docker Desktop (or OrbStack) — the app's guide has a "Start Docker" button. Linux: `sudo systemctl enable --now docker`. |
| `~/.local/bin is not on your PATH` | Add `export PATH="$HOME/.local/bin:$PATH"` to `~/.zshrc` or `~/.bashrc`, or use the full path in your MCP client config. |

## Manual setup

If you'd rather not use the scripts:

```sh
# prerequisites: Go 1.26+, Bun 1.4+, Rust (rustup), plus the OS libraries above
cd apps/desktop
bun install --frozen-lockfile
bun run tauri:build      # builds the sidecar, the UI and the app bundle
bun run tauri:dev        # or: development mode

cd ../../backend
go build -o bin/nimbo ./cmd/nimbo && ./bin/nimbo doctor
```
