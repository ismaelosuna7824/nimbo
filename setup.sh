#!/usr/bin/env bash
# Nimbo setup: checks/installs prerequisites, installs dependencies and builds
# the `nimbo` backend and the desktop app. macOS and Linux (incl. WSL2).
# Run ./setup.sh help for usage; docs/SETUP.md has the details.
# Must stay compatible with macOS's /bin/bash 3.2.
set -euo pipefail

usage() {
	cat <<'EOF'
Usage: ./setup.sh [command] [--yes] [--no-install]

Commands:
  install   (default) check/install prerequisites, install dependencies, build
            the backend and the desktop app, offer to install it, run `nimbo doctor`
  dev       check prerequisites, install dependencies, build the backend, then
            start the app in development mode (bun run tauri dev)
  build     install dependencies and build everything (assumes prerequisites)
  doctor    build the backend if needed and run `nimbo doctor` (add --json for JSON)
  help      show this help

Options:
  -y, --yes         answer "yes" to every question (unattended install)
  -n, --no-install  never install or change anything outside this folder: missing
                    prerequisites are only reported, and the offers to copy the
                    app to /Applications and to link the CLI are skipped
EOF
}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
BACKEND="$ROOT/backend"
DESKTOP="$ROOT/apps/desktop"
TAURI_DIR="$DESKTOP/src-tauri"
NIMBO_BIN="$BACKEND/bin/nimbo"
MIN_GO="1.26"
MIN_BUN="1.4"
MIN_RUST="$(sed -n 's/^rust-version *= *"\([0-9.]*\)".*/\1/p' "$TAURI_DIR/Cargo.toml" 2>/dev/null)"
MIN_RUST="${MIN_RUST:-1.90}"
TAURI_PREREQS_URL="https://v2.tauri.app/start/prerequisites/"

# ---------------------------------------------------------------- arguments --
MODE=""
ASSUME_YES=0
NO_INSTALL=0
DOCTOR_ARGS=()
while (($#)); do
	case "$1" in
	install | dev | build | doctor | help)
		[[ -z "$MODE" ]] || { echo "setup.sh: only one command allowed (got '$MODE' and '$1')" >&2; exit 2; }
		MODE="$1"
		;;
	-h | --help) MODE="help" ;;
	-y | --yes) ASSUME_YES=1 ;;
	-n | --no-install) NO_INSTALL=1 ;;
	--json) DOCTOR_ARGS+=("--json") ;;
	*)
		echo "setup.sh: unknown argument '$1'" >&2
		usage >&2
		exit 2
		;;
	esac
	shift
done
MODE="${MODE:-install}"
if ((ASSUME_YES && NO_INSTALL)); then
	echo "setup.sh: --yes and --no-install contradict each other; pick one" >&2
	exit 2
fi
if ((${#DOCTOR_ARGS[@]})) && [[ "$MODE" != doctor ]]; then
	echo "setup.sh: --json only applies to './setup.sh doctor'" >&2
	exit 2
fi
if [[ "$MODE" == help ]]; then
	usage
	exit 0
fi

# ------------------------------------------------------------------- output --
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
	BOLD=$'\033[1m' DIM=$'\033[2m' RED=$'\033[31m' GREEN=$'\033[32m' YELLOW=$'\033[33m' BLUE=$'\033[34m' RESET=$'\033[0m'
else
	BOLD="" DIM="" RED="" GREEN="" YELLOW="" BLUE="" RESET=""
fi

fmt_time() {
	local s=$1
	if ((s >= 60)); then printf '%dm %02ds' $((s / 60)) $((s % 60)); else printf '%ds' "$s"; fi
}
info() { printf '  %s\n' "$*"; }
note() { printf '  %s%s%s\n' "$DIM" "$*" "$RESET"; }
warn() { printf '  %s!%s %s\n' "$YELLOW" "$RESET" "$*"; }
die() {
	STEP_NAME="" # the message below already explains the failure
	printf '\n%s✗ %s%s\n' "$RED$BOLD" "$1" "$RESET" >&2
	shift
	local line
	for line in "$@"; do printf '  %s\n' "$line" >&2; done
	exit 1
}
# Prerequisite rows: name, status text.
row_ok() { printf '  %s✓%s %-28s %s%s%s\n' "$GREEN" "$RESET" "$1" "$DIM" "$2" "$RESET"; }
row_bad() { printf '  %s✗%s %-28s %s\n' "$RED" "$RESET" "$1" "$2"; }
row_warn() { printf '  %s!%s %-28s %s\n' "$YELLOW" "$RESET" "$1" "$2"; }

START_TIME=$SECONDS
STEP_NO=0
STEP_TOTAL=0
STEP_NAME=""
STEP_START=0
step_close() {
	[[ -n "$STEP_NAME" ]] || return 0
	printf '  %s✓ %s done in %s%s\n' "$DIM" "$STEP_NAME" "$(fmt_time $((SECONDS - STEP_START)))" "$RESET"
	STEP_NAME=""
}
step() {
	step_close
	STEP_NO=$((STEP_NO + 1))
	STEP_NAME="$1"
	STEP_START=$SECONDS
	printf '\n%s[%d/%d] %s%s %s(%s elapsed)%s\n' "$BOLD$BLUE" "$STEP_NO" "$STEP_TOTAL" "$1" "$RESET" "$DIM" "$(fmt_time $((SECONDS - START_TIME)))" "$RESET"
}
on_exit() {
	local rc=$?
	if ((rc != 0)) && [[ -n "$STEP_NAME" ]]; then
		printf '\n%s✗ Step "%s" failed after %s.%s See the messages above; docs/SETUP.md has troubleshooting tips.\n' \
			"$RED" "$STEP_NAME" "$(fmt_time $((SECONDS - STEP_START)))" "$RESET" >&2
	fi
}
trap on_exit EXIT

# Prints a command, then runs it.
run() {
	printf '  %s$ %s%s\n' "$DIM" "$*" "$RESET"
	"$@"
}

# ask QUESTION → 0 for yes. Honors --yes / --no-install; never blocks without a terminal.
ask() {
	if ((NO_INSTALL)); then
		note "$1 → skipped (--no-install)"
		return 1
	fi
	if ((ASSUME_YES)); then
		printf '  %s [Y/n] y (--yes)\n' "$1"
		return 0
	fi
	if [[ ! -t 0 ]]; then
		warn "$1 → skipped (no terminal to ask; re-run with --yes to accept)"
		return 1
	fi
	local reply
	printf '  %s%s [Y/n]%s ' "$BOLD" "$1" "$RESET"
	read -r reply || return 1
	case "$reply" in "" | y | Y | yes | Yes | YES) return 0 ;; *) return 1 ;; esac
}

# offer WHAT CMD... : shows exactly what would run, then asks.
offer() {
	local what=$1 line
	shift
	info "To install $what this will run:"
	for line in "$@"; do printf '    %s%s%s\n' "$DIM" "$line" "$RESET"; done
	ask "Install $what now?"
}

# ver_ge A B → A >= B, comparing up to three numeric parts ("1.26.0" >= "1.26").
ver_ge() {
	local IFS=. i x y
	local -a a b
	read -r -a a <<<"$1"
	read -r -a b <<<"$2"
	for i in 0 1 2; do
		x="${a[i]:-0}" y="${b[i]:-0}"
		x="${x%%[!0-9]*}" y="${y%%[!0-9]*}"
		x="${x:-0}" y="${y:-0}"
		((10#$x > 10#$y)) && return 0
		((10#$x < 10#$y)) && return 1
	done
	return 0
}

# with_timeout SECONDS CMD... : runs CMD, killing it after SECONDS (macOS has no timeout(1)).
with_timeout() {
	local secs=$1 pid watcher rc=0
	shift
	"$@" &
	pid=$!
	(sleep "$secs" && kill "$pid") >/dev/null 2>&1 &
	watcher=$!
	wait "$pid" || rc=$?
	kill "$watcher" 2>/dev/null || true
	wait "$watcher" 2>/dev/null || true
	return "$rc"
}

# ------------------------------------------------------------------ environment --
path_prepend() { [[ -d "$1" ]] && case ":$PATH:" in *":$1:"*) ;; *) PATH="$1:$PATH" ;; esac; return 0; }
path_append() { [[ -d "$1" ]] && case ":$PATH:" in *":$1:"*) ;; *) PATH="$PATH:$1" ;; esac; return 0; }

# Makes tools installed earlier visible even in a fresh shell whose profile
# does not set them up (e.g. a new terminal right after an installer ran).
# Only appends, so the user's own PATH order still decides which copy wins;
# installers in this run prepend their directory explicitly.
refresh_path() {
	path_append /opt/homebrew/bin
	path_append /usr/local/bin
	path_append /usr/local/go/bin
	path_append "$HOME/.bun/bin"
	path_append "$HOME/.cargo/bin"
	# Docker CLIs installed by the common engines (only used for the Docker check).
	path_append "$HOME/.orbstack/bin"
	path_append /Applications/Docker.app/Contents/Resources/bin
	path_append "$HOME/.docker/bin"
	path_append "$HOME/.rd/bin"
	path_append "$HOME/.colima/bin"
	export PATH
	hash -r
}

# After an installer ran in this process: put its tools first.
use_bun_install() {
	path_prepend "$HOME/.bun/bin"
	export PATH
	hash -r
}
use_rustup_install() {
	if [[ -f "$HOME/.cargo/env" ]]; then
		# shellcheck source=/dev/null
		. "$HOME/.cargo/env"
	fi
	path_prepend "$HOME/.cargo/bin"
	export PATH
	hash -r
}

have() { command -v "$1" >/dev/null 2>&1; }

OS=""
ARCH=""
PM=""
SUDO=""
IS_WSL=0
detect_system() {
	case "$(uname -s)" in
	Darwin) OS=macos ;;
	Linux) OS=linux ;;
	MINGW* | MSYS* | CYGWIN*)
		die "Windows is not supported by setup.sh directly." \
			"Use WSL2 (Ubuntu) and run ./setup.sh there, or follow the Windows steps in docs/SETUP.md."
		;;
	*) die "Unsupported operating system: $(uname -s)" "See docs/SETUP.md for the manual steps." ;;
	esac
	ARCH="$(uname -m)"
	if [[ "$OS" == linux ]] && grep -qi microsoft /proc/version 2>/dev/null; then IS_WSL=1; fi
	if [[ "$OS" == linux ]]; then
		if have apt-get; then PM=apt; elif have dnf; then PM=dnf; elif have pacman; then PM=pacman; elif have zypper; then PM=zypper; fi
	fi
	if [[ "$(id -u)" == 0 ]]; then
		[[ "$OS" == macos ]] && die "Do not run setup.sh with sudo on macOS." "Run it as your normal user; it asks before anything needs more rights."
		SUDO=""
	elif have sudo; then
		SUDO="sudo"
	fi
}

describe_system() {
	local desc
	if [[ "$OS" == macos ]]; then
		desc="macOS $(sw_vers -productVersion 2>/dev/null || echo '?')"
	else
		desc="Linux"
		if [[ -r /etc/os-release ]]; then
			# shellcheck source=/dev/null
			desc="$(. /etc/os-release && echo "${PRETTY_NAME:-Linux}")"
		fi
		((IS_WSL)) && desc="$desc (WSL)"
	fi
	row_ok "System" "$desc, $ARCH"
	if [[ "$OS" == linux ]]; then
		if [[ -n "$PM" ]]; then
			row_ok "Package manager" "$PM"
		else
			row_warn "Package manager" "none of apt/dnf/pacman/zypper found — install the Tauri libraries yourself: $TAURI_PREREQS_URL"
		fi
		((IS_WSL)) && note "WSL: the desktop window needs WSLg (Windows 11 or recent Windows 10)."
	fi
	row_ok "Repository" "$ROOT"
}

# Appends LINE to the user's shell profile (once) so new terminals find a tool.
persist_line() {
	local line=$1 profile
	case "${SHELL:-}" in
	*/zsh) profile="$HOME/.zprofile" ;;
	*/bash) if [[ "$OS" == macos ]]; then profile="$HOME/.bash_profile"; else profile="$HOME/.bashrc"; fi ;;
	*) profile="$HOME/.profile" ;;
	esac
	if [[ -f "$profile" ]] && grep -qxF "$line" "$profile"; then return 0; fi
	info "Adding to $profile so new terminals find it:"
	printf '    %s%s%s\n' "$DIM" "$line" "$RESET"
	printf '\n%s\n' "$line" >>"$profile"
}

# sudo wrapper that refuses to escalate invisibly: callers always print the command first.
as_root() {
	if [[ "$(id -u)" == 0 ]]; then
		"$@"
	elif [[ -n "$SUDO" ]]; then
		run sudo "$@"
	else
		die "This step needs administrator rights but 'sudo' is not available." "Run as root: $*"
	fi
}

# -------------------------------------------------------------- prerequisites --
MISSING=()          # required items still missing after the checks
WAS_INSTALLED=0     # something was installed in this run

need_missing() { MISSING+=("$1"); }

check_xcode_clt() {
	local name="Xcode Command Line Tools" waited=0
	if xcode-select -p >/dev/null 2>&1; then
		row_ok "$name" "$(xcode-select -p)"
		return
	fi
	row_bad "$name" "not installed (Apple's compilers; needed to build the app)"
	if offer "the Xcode Command Line Tools" "xcode-select --install   (opens Apple's installer window)"; then
		run xcode-select --install || true
		info "Finish the installer window that just opened; waiting for it (up to 30 minutes)…"
		until xcode-select -p >/dev/null 2>&1; do
			((waited >= 1800)) && die "The Xcode Command Line Tools are still missing." "Finish Apple's installer, then run ./setup.sh again."
			sleep 10
			waited=$((waited + 10))
		done
		WAS_INSTALLED=1
		row_ok "$name" "$(xcode-select -p)"
	else
		need_missing "$name — run: xcode-select --install"
	fi
}

brew_shellenv() {
	local b
	for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do
		if [[ -x "$b" ]]; then
			eval "$("$b" shellenv)"
			return 0
		fi
	done
	return 1
}

check_homebrew() {
	local name="Homebrew"
	have brew || brew_shellenv || true
	if have brew; then
		row_ok "$name" "$(brew --version 2>/dev/null | sed -n 1p)"
		return
	fi
	row_bad "$name" "not installed (macOS package manager; used to install Go)"
	# shellcheck disable=SC2016 # shown to the user verbatim; the real call is below
	local cmd='/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"'
	if offer "Homebrew (asks for your Mac password)" "$cmd"; then
		printf '  %s$ %s%s\n' "$DIM" "$cmd" "$RESET"
		/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
		brew_shellenv || die "Homebrew was installed but 'brew' was not found." "Open a new terminal and run ./setup.sh again."
		persist_line "eval \"\$($(command -v brew) shellenv)\""
		WAS_INSTALLED=1
		row_ok "$name" "$(brew --version 2>/dev/null | sed -n 1p)"
	else
		need_missing "Homebrew — see https://brew.sh"
	fi
}

GO_VERSION=""
go_effective_version() {
	# Inside backend/, GOTOOLCHAIN=auto lets an older Go fetch the version go.mod asks for.
	GO_VERSION=""
	have go || return 1
	GO_VERSION="$(cd "$BACKEND" && go env GOVERSION 2>/dev/null)" || GO_VERSION=""
	GO_VERSION="${GO_VERSION#go}"
	[[ -n "$GO_VERSION" ]] && ver_ge "$GO_VERSION" "$MIN_GO"
}

install_go() {
	if [[ "$OS" == macos ]]; then
		if have brew; then
			offer "Go" "brew install go" || return 1
			if brew list --formula go >/dev/null 2>&1; then run brew upgrade go || true; else run brew install go; fi
		else
			info "Go needs Homebrew here (or the installer from https://go.dev/dl/)."
			return 1
		fi
	else
		local arch latest
		case "$ARCH" in
		x86_64 | amd64) arch=amd64 ;;
		aarch64 | arm64) arch=arm64 ;;
		*) info "No official Go build for $ARCH; see https://go.dev/dl/"; return 1 ;;
		esac
		have curl || { info "curl is needed to download Go (install the system libraries first)."; return 1; }
		latest="$(curl -fsSL 'https://go.dev/VERSION?m=text')" || { info "Could not reach go.dev."; return 1; }
		latest="${latest%%$'\n'*}"
		offer "Go ($latest, official tarball into /usr/local/go)" \
			"curl -fsSLo /tmp/$latest.tar.gz https://go.dev/dl/$latest.linux-$arch.tar.gz" \
			"$SUDO rm -rf /usr/local/go && $SUDO tar -C /usr/local -xzf /tmp/$latest.tar.gz" || return 1
		run curl -fsSLo "/tmp/$latest.tar.gz" "https://go.dev/dl/$latest.linux-$arch.tar.gz"
		as_root rm -rf /usr/local/go
		as_root tar -C /usr/local -xzf "/tmp/$latest.tar.gz"
		rm -f "/tmp/$latest.tar.gz"
		# shellcheck disable=SC2016 # expanded by the user's shell, not here
		persist_line 'export PATH="$PATH:/usr/local/go/bin"'
		path_prepend /usr/local/go/bin
		export PATH
	fi
	hash -r
}

check_go() {
	local name="Go $MIN_GO+" base
	if go_effective_version; then
		base="$(go env GOVERSION 2>/dev/null)"
		if [[ "go$GO_VERSION" == "$base" ]]; then
			row_ok "$name" "go$GO_VERSION"
		else
			row_ok "$name" "go$GO_VERSION (auto toolchain; installed $base)"
		fi
		return
	fi
	if have go; then
		row_bad "$name" "found $(go env GOVERSION 2>/dev/null), too old (and could not switch toolchains)"
	else
		row_bad "$name" "not installed (compiles the Nimbo backend)"
	fi
	if install_go && go_effective_version; then
		WAS_INSTALLED=1
		row_ok "$name" "go$GO_VERSION"
	else
		need_missing "Go $MIN_GO or newer — https://go.dev/dl/"
	fi
}

bun_version() { have bun && bun --version 2>/dev/null; }

# Other Bun copies at least MIN_BUN that are NOT the one on PATH (reported, never switched to).
other_new_buns() {
	local chosen=$1 c v seen=" "
	for c in $(type -ap bun 2>/dev/null) "$HOME/.bun/bin/bun" /opt/homebrew/bin/bun /usr/local/bin/bun; do
		[[ -x "$c" && "$c" != "$chosen" && "$seen" != *" $c "* ]] || continue
		seen="$seen$c "
		v="$("$c" --version 2>/dev/null || true)"
		if [[ -n "$v" ]] && ver_ge "$v" "$MIN_BUN"; then printf '%s %s\n' "$v" "$c"; fi
	done
}

check_bun() {
	local name="Bun $MIN_BUN+" v path real other="" cmd ov op
	v="$(bun_version || true)"
	path="$(command -v bun || true)"
	if [[ -n "$v" ]] && ver_ge "$v" "$MIN_BUN"; then
		row_ok "$name" "$v ($path)"
		return
	fi
	if [[ -n "$v" ]]; then
		row_bad "$name" "found $v at $path (first on PATH), too old"
		while IFS=' ' read -r ov op; do
			[[ -n "$ov" ]] || continue
			other=1
			info "A newer Bun $ov is installed at $op, but $path comes first on your PATH."
			info "Put it first (e.g. export PATH=\"$(dirname "$op"):\$PATH\" in your shell profile) or upgrade/remove $path."
		done < <(other_new_buns "$path")
		real="$(cd "$(dirname "$path")" && cd "$(dirname "$(readlink "$path" || echo "$path")")" && pwd -P)" || real=""
		case "$real" in
		*/Cellar/*) cmd="brew upgrade $(basename "$(dirname "$(dirname "$real")")")" ;;
		"$HOME/.bun/"*) cmd="bun upgrade" ;;
		*) cmd="" ;;
		esac
		if [[ -n "$cmd" ]]; then
			# shellcheck disable=SC2086 # cmd is a plain word list built above
			if offer "a newer Bun (upgrading $path)" "$cmd"; then run $cmd; fi
		elif [[ -z "${other:-}" ]]; then
			info "Upgrade it with the tool that installed it, or see https://bun.sh"
		fi
	else
		row_bad "$name" "not installed (JavaScript runtime used to build the app)"
		if offer "Bun (official installer from bun.sh)" "curl -fsSL https://bun.sh/install | bash"; then
			have curl || die "curl is required to install Bun." "Install curl with your package manager, then run ./setup.sh again."
			printf '  %s$ curl -fsSL https://bun.sh/install | bash%s\n' "$DIM" "$RESET"
			curl -fsSL https://bun.sh/install | bash
			use_bun_install
		fi
	fi
	hash -r
	v="$(bun_version || true)"
	path="$(command -v bun || true)"
	if [[ -n "$v" ]] && ver_ge "$v" "$MIN_BUN"; then
		WAS_INSTALLED=1
		row_ok "$name" "$v ($path)"
	else
		need_missing "Bun $MIN_BUN or newer — https://bun.sh (the bun first on your PATH is used)"
	fi
}

rust_version() {
	have rustc && have cargo || return 1
	rustc --version 2>/dev/null | sed -n 's/^rustc \([0-9][0-9.]*\).*/\1/p'
}

check_rust() {
	local name="Rust $MIN_RUST+" v
	v="$(rust_version || true)"
	if [[ -n "$v" ]] && ver_ge "$v" "$MIN_RUST"; then
		row_ok "$name" "rustc $v$(have rustup && printf ', via rustup' || true)"
		return
	fi
	if [[ -n "$v" ]] && have rustup; then
		row_bad "$name" "found rustc $v, too old"
		if offer "the latest stable Rust" "rustup update stable"; then run rustup update stable; fi
	elif [[ -n "$v" ]]; then
		row_bad "$name" "found rustc $v (not from rustup), too old"
		info "Update it with the tool that installed it, or remove it and use https://rustup.rs"
	else
		row_bad "$name" "not installed (compiles the desktop app shell)"
		if offer "Rust (official rustup installer)" "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y"; then
			have curl || die "curl is required to install Rust." "Install curl with your package manager, then run ./setup.sh again."
			printf "  %s\$ curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y%s\n" "$DIM" "$RESET"
			curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
			use_rustup_install
		fi
	fi
	hash -r
	v="$(rust_version || true)"
	if [[ -n "$v" ]] && ver_ge "$v" "$MIN_RUST"; then
		WAS_INSTALLED=1
		row_ok "$name" "rustc $v"
	else
		need_missing "Rust $MIN_RUST or newer — https://rustup.rs"
	fi
}

# Linux system libraries for Tauri, per $TAURI_PREREQS_URL.
pkg_installed() {
	case "$PM" in
	apt) [[ "$(dpkg-query -W -f='${Status}' "$1" 2>/dev/null)" == "install ok installed" ]] ;;
	dnf | zypper) rpm -q "$1" >/dev/null 2>&1 ;;
	pacman) pacman -Q "$1" >/dev/null 2>&1 ;;
	*) return 1 ;;
	esac
}

check_linux_libs() {
	local name="Tauri system libraries" pkg indicator p
	local missing=() pkgs=() group="" cmds=()
	if [[ -z "$PM" ]]; then
		row_bad "$name" "unknown package manager"
		need_missing "Tauri system libraries — install them for your distribution: $TAURI_PREREQS_URL"
		return
	fi
	case "$PM" in
	apt)
		for pkg in libwebkit2gtk-4.1-dev build-essential curl wget file libxdo-dev libssl-dev librsvg2-dev; do
			pkg_installed "$pkg" || pkgs+=("$pkg")
		done
		if ! pkg_installed libayatana-appindicator3-dev && ! pkg_installed libappindicator3-dev; then
			# Tauri's default; older releases (before Ubuntu 20.04/Debian 11) only ship libappindicator3-dev.
			indicator=libayatana-appindicator3-dev
			if ! apt-cache show "$indicator" >/dev/null 2>&1 && apt-cache show libappindicator3-dev >/dev/null 2>&1; then
				indicator=libappindicator3-dev
			fi
			pkgs+=("$indicator")
		fi
		;;
	dnf)
		for pkg in webkit2gtk4.1-devel openssl-devel curl wget file libappindicator-gtk3-devel librsvg2-devel libxdo-devel; do
			pkg_installed "$pkg" || pkgs+=("$pkg")
		done
		{ have gcc && have make; } || group="c-development"
		;;
	pacman)
		for pkg in webkit2gtk-4.1 base-devel curl wget file openssl appmenu-gtk-module libappindicator-gtk3 librsvg xdotool; do
			pkg_installed "$pkg" || pkgs+=("$pkg")
		done
		;;
	zypper)
		for pkg in webkit2gtk3-devel libopenssl-devel curl wget file libappindicator3-1 librsvg-devel; do
			pkg_installed "$pkg" || pkgs+=("$pkg")
		done
		{ have gcc && have make; } || group="devel_basis"
		;;
	esac
	missing=(${pkgs[@]+"${pkgs[@]}"})
	[[ -z "$group" ]] || missing+=("$group (compilers)")
	if ((${#missing[@]} == 0)); then
		row_ok "$name" "all present ($PM)"
		return
	fi
	row_bad "$name" "missing: ${missing[*]}"
	case "$PM" in
	apt) cmds=("apt-get update" "apt-get install -y ${pkgs[*]}") ;;
	dnf)
		if ((${#pkgs[@]})); then cmds+=("dnf install -y ${pkgs[*]}"); fi
		if [[ -n "$group" ]]; then cmds+=("dnf group install -y $group"); fi
		;;
	pacman) cmds=("pacman -S --needed --noconfirm ${pkgs[*]}") ;;
	zypper)
		if ((${#pkgs[@]})); then cmds+=("zypper --non-interactive install ${pkgs[*]}"); fi
		if [[ -n "$group" ]]; then cmds+=("zypper --non-interactive install -t pattern $group"); fi
		;;
	esac
	if [[ "$(id -u)" != 0 && -z "$SUDO" ]]; then
		info "'sudo' is not available; run these as root, then run ./setup.sh again:"
		for p in "${cmds[@]}"; do printf '    %s\n' "$p"; done
		need_missing "Tauri system libraries (${missing[*]})"
		return
	fi
	local shown=()
	for p in "${cmds[@]}"; do shown+=("${SUDO:+$SUDO }$p"); done
	if offer "the Tauri system libraries${SUDO:+ (sudo asks for your password)}" "${shown[@]}"; then
		for p in "${cmds[@]}"; do
			# Each entry is a plain word list built above (no quoting inside).
			# shellcheck disable=SC2086
			as_root $p
		done
		WAS_INSTALLED=1
		row_ok "$name" "installed ($PM)"
	else
		need_missing "Tauri system libraries (${missing[*]}) — $TAURI_PREREQS_URL"
	fi
}

check_docker() {
	local name="Docker (recommended)" out
	if ! have docker; then
		row_warn "$name" "not found — not needed to build; the app's first-run guide helps you install it"
		return
	fi
	if out="$(with_timeout 8 docker version --format '{{.Server.Version}}' 2>&1)" && [[ -n "$out" ]]; then
		local ctx
		out="${out##*$'\n'}"
		ctx="$(docker context show 2>/dev/null || true)"
		row_ok "$name" "engine $out${ctx:+ (context: $ctx)}, CLI $(command -v docker)"
	elif [[ "$OS" == linux && "$out" == *"permission denied"* ]]; then
		row_warn "$name" "engine running but your user may not use it yet"
		# shellcheck disable=SC2016 # printed for the user
		note 'Fix: sudo usermod -aG docker "$USER", then log out and back in (the app guide explains this too).'
	else
		if [[ "$OS" == macos ]]; then
			row_warn "$name" "CLI found but the engine is not running — open Docker Desktop/OrbStack (the app can start it for you)"
		else
			row_warn "$name" "CLI found but the engine is not running — sudo systemctl enable --now docker"
		fi
	fi
}

prerequisites() {
	refresh_path
	if [[ "$OS" == macos ]]; then
		check_xcode_clt
		check_homebrew
	else
		check_linux_libs
	fi
	check_go
	check_bun
	check_rust
	check_docker
	if ((${#MISSING[@]})); then
		local lines=() m
		for m in "${MISSING[@]}"; do lines+=("• $m"); done
		lines+=("" "Install them (or re-run ./setup.sh without --no-install and answer yes), then run ./setup.sh again.")
		lines+=("Step-by-step help: docs/SETUP.md")
		die "Missing prerequisites:" "${lines[@]}"
	fi
	if ((WAS_INSTALLED)); then
		note "Tools were installed in this run; open a new terminal later so your shell picks them up too."
	fi
}

# Quick presence check for `build`, which skips installing prerequisites.
require_tools() {
	refresh_path
	local t missing=()
	for t in go bun cargo rustc; do have "$t" || missing+=("$t"); done
	((${#missing[@]} == 0)) || die "Missing tools: ${missing[*]}" "Run ./setup.sh (install) first; it checks and installs prerequisites."
	go_effective_version || die "Go $MIN_GO or newer is required (found ${GO_VERSION:-none})." "Run ./setup.sh to install it."
}

# ------------------------------------------------------------------- builds --
VERSION=""
resolve_version() {
	VERSION="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || true)"
	if [[ -z "$VERSION" ]]; then
		VERSION="$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$TAURI_DIR/tauri.conf.json" | sed -n 1p)-dev"
	fi
	export NIMBO_VERSION="$VERSION"
}

install_deps() {
	cd "$DESKTOP"
	if [[ -f bun.lock || -f bun.lockb ]] && run bun install --frozen-lockfile; then
		return 0
	fi
	[[ -f bun.lock || -f bun.lockb ]] && warn "The lockfile is out of date with package.json; installing without --frozen-lockfile."
	run bun install
}

build_backend() {
	cd "$BACKEND"
	mkdir -p bin
	run go build -trimpath -ldflags "-X main.version=$VERSION" -o "$NIMBO_BIN" ./cmd/nimbo
	row_ok "backend/bin/nimbo" "$("$NIMBO_BIN" version)"
}

backend_stale() {
	[[ -x "$NIMBO_BIN" ]] || return 0
	[[ -n "$(find "$BACKEND" \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -newer "$NIMBO_BIN" -print 2>/dev/null | sed -n 1p)" ]]
}

build_sidecar() {
	cd "$DESKTOP"
	run bun run sidecar
}

BUNDLES=()
bundle_dir() {
	local target="${CARGO_TARGET_DIR:-$TAURI_DIR/target}"
	[[ "$target" == /* ]] || target="$TAURI_DIR/$target"
	printf '%s/release/bundle' "$target"
}

build_app() {
	local marker dir b
	cd "$DESKTOP"
	marker="$(mktemp "${TMPDIR:-/tmp}/nimbo-setup.XXXXXX")"
	# Xcode's strip on macOS 27 corrupts proc-macro dylibs ("mis-aligned
	# LINKEDIT string pool"), so clean release builds fail to load them.
	# Keep Cargo from stripping unless the caller chose otherwise.
	if [[ "$OS" == macos ]]; then export CARGO_PROFILE_RELEASE_STRIP="${CARGO_PROFILE_RELEASE_STRIP:-none}"; fi
	run bun run tauri build
	dir="$(bundle_dir)"
	BUNDLES=()
	while IFS= read -r b; do
		[[ -n "$b" ]] && BUNDLES+=("$b")
	done < <(find "$dir" -mindepth 2 -maxdepth 2 \( -name '*.app' -o -name '*.dmg' -o -name '*.deb' -o -name '*.rpm' -o -name '*.AppImage' \) -newer "$marker" -print 2>/dev/null | sort)
	rm -f "$marker"
	((${#BUNDLES[@]})) || die "The build finished but no bundle was found in $dir." "Check the Tauri output above."
	info "${BOLD}Built:${RESET}"
	for b in "${BUNDLES[@]}"; do printf '    %s\n' "$b"; done
}

# ------------------------------------------------------------ install offers --
INSTALLED_APP=""
APP_SIDECAR_REL="Contents/MacOS/nimbo"

install_app_macos() {
	local app="" b dest
	for b in "${BUNDLES[@]}"; do [[ "$b" == *.app ]] && app="$b"; done
	[[ -n "$app" ]] || return 0
	dest="/Applications"
	[[ -w "$dest" ]] || dest="$HOME/Applications"
	local target
	target="$dest/$(basename "$app")"
	if [[ -d "$target" ]]; then
		info "$target already exists; it will be replaced (quit Nimbo first if it is open)."
	fi
	if ask "Copy $(basename "$app") to $dest?"; then
		mkdir -p "$dest"
		[[ -d "$target" ]] && run rm -rf "$target"
		run ditto "$app" "$target"
		INSTALLED_APP="$target"
		row_ok "Installed app" "$target"
	elif [[ -x "/Applications/$(basename "$app")/$APP_SIDECAR_REL" ]]; then
		INSTALLED_APP="/Applications/$(basename "$app")"
	fi
}

link_cli() {
	local link_dir="$HOME/.local/bin" target="$NIMBO_BIN"
	if [[ -n "$INSTALLED_APP" && -x "$INSTALLED_APP/$APP_SIDECAR_REL" ]]; then
		target="$INSTALLED_APP/$APP_SIDECAR_REL"
	fi
	info "The 'nimbo' command lets AI agents use Nimbo over MCP (see docs/MCP.md)."
	if ask "Link the nimbo command into $link_dir (→ $target)?"; then
		mkdir -p "$link_dir"
		run ln -sfn "$target" "$link_dir/nimbo"
		row_ok "CLI" "$link_dir/nimbo → $target"
		case ":$PATH:" in
		*":$link_dir:"*) ;;
		*)
			warn "$link_dir is not on your PATH. Add this line to your shell profile:"
			# shellcheck disable=SC2016 # printed for the user
			printf '    %s\n' 'export PATH="$HOME/.local/bin:$PATH"'
			;;
		esac
	fi
}

offer_installs() {
	if [[ "$OS" == macos ]]; then
		install_app_macos
	else
		local b
		for b in "${BUNDLES[@]}"; do
			case "$b" in
			*.deb) info "Install the .deb with: sudo apt install '$b'" ;;
			*.rpm) info "Install the .rpm with: sudo dnf install '$b'" ;;
			*.AppImage) info "Or run the AppImage directly: '$b'" ;;
			esac
		done
	fi
	link_cli
}

# ------------------------------------------------------------------ doctor --
DOCTOR_RC=0
run_doctor() {
	DOCTOR_RC=0
	"$NIMBO_BIN" doctor ${DOCTOR_ARGS[@]+"${DOCTOR_ARGS[@]}"} || DOCTOR_RC=$?
	if ((DOCTOR_RC == 2)); then
		warn "This nimbo build has no 'doctor' command; rebuild with ./setup.sh build."
	fi
}

next_steps() {
	local app=""
	printf '\n%s%s✓ Nimbo is built%s in %s.\n\n' "$GREEN" "$BOLD" "$RESET" "$(fmt_time $((SECONDS - START_TIME)))"
	printf '%sNext steps%s\n' "$BOLD" "$RESET"
	if [[ -n "$INSTALLED_APP" ]]; then
		app="open \"$INSTALLED_APP\"  (or find Nimbo in Launchpad/Spotlight)"
	elif [[ "$OS" == macos ]]; then
		local b
		for b in "${BUNDLES[@]}"; do [[ "$b" == *.app ]] && app="open \"$b\""; done
	else
		local b
		for b in "${BUNDLES[@]}"; do [[ "$b" == *.AppImage ]] && app="\"$b\""; done
		[[ -n "$app" ]] || app="install the package listed above and start Nimbo from your app menu"
	fi
	info "1. Open the app: $app"
	info "2. The first-run guide checks Docker and the sandbox images and fixes what it can."
	if ((DOCTOR_RC != 0)); then
		info "   (nimbo doctor above lists what is still missing — the guide walks you through it.)"
	fi
	info "3. Development mode with hot reload: ./setup.sh dev"
	info "Check this computer again any time: ./setup.sh doctor"
}

# -------------------------------------------------------------------- main --
cd "$ROOT"
detect_system
# doctor keeps stdout for nimbo's own report (it may be --json).
[[ "$MODE" == doctor ]] || printf '%sNimbo setup%s — %s %s(%s)%s\n' "$BOLD" "$RESET" "$MODE" "$DIM" "$ROOT" "$RESET"

case "$MODE" in
install)
	STEP_TOTAL=8
	step "Detecting this computer"
	describe_system
	step "Checking prerequisites"
	prerequisites
	resolve_version
	step "Installing JavaScript dependencies"
	install_deps
	step "Building the backend ($VERSION)"
	build_backend
	step "Building the app's bundled backend (sidecar)"
	build_sidecar
	step "Building the desktop app (takes a few minutes the first time)"
	build_app
	step "Installing"
	offer_installs
	step "Checking Docker and sandbox readiness (nimbo doctor)"
	run_doctor
	step_close
	next_steps
	;;
dev)
	STEP_TOTAL=6
	step "Detecting this computer"
	describe_system
	step "Checking prerequisites"
	prerequisites
	resolve_version
	step "Installing JavaScript dependencies"
	install_deps
	step "Building the backend ($VERSION)"
	build_backend
	step "Building the app's bundled backend (sidecar)"
	build_sidecar
	step "Starting the app in development mode (Ctrl+C to stop)"
	step_close
	cd "$DESKTOP"
	trap - EXIT
	run exec bun run tauri dev
	;;
build)
	STEP_TOTAL=4
	require_tools
	resolve_version
	step "Installing JavaScript dependencies"
	install_deps
	step "Building the backend ($VERSION)"
	build_backend
	step "Building the app's bundled backend (sidecar)"
	build_sidecar
	step "Building the desktop app"
	build_app
	step_close
	printf '\n%s✓ Build finished%s in %s.\n' "$GREEN$BOLD" "$RESET" "$(fmt_time $((SECONDS - START_TIME)))"
	;;
doctor)
	refresh_path
	if backend_stale; then
		{
			have go || die "The nimbo backend is not built and Go is missing." "Run ./setup.sh first."
			go_effective_version || die "Go $MIN_GO or newer is required to build the backend." "Run ./setup.sh first."
			resolve_version
			STEP_TOTAL=1
			step "Building the backend ($VERSION)"
			build_backend
			step_close
			echo
		} >&2
	fi
	run_doctor
	exit "$DOCTOR_RC"
	;;
esac
