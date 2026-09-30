#!/usr/bin/env bash
# Install, upgrade or remove pasture as a systemd USER service.
# Full guide: SYSTEMD.md (next to this file).
#
#   ./install-systemd.sh                 build, install, enable and start
#   ./install-systemd.sh --linger        ... and keep it running after logout
#   ./install-systemd.sh --restart       upgrade and restart a running server
#                                        (ENDS every program running on it)
#   ./install-systemd.sh --bin FILE      install a prebuilt binary instead of building
#   ./install-systemd.sh --uninstall     stop, disable and remove the unit
#   ./install-systemd.sh --uninstall --purge   ... and the binary, settings and logs
#   ./install-systemd.sh --dry-run ...   print what would be done
#
# Installs:
#   ~/.local/bin/pasture                     the server
#   ~/.config/systemd/user/pasture.service   the unit
#   ~/.config/pasture/pasture.env            settings (kept if it exists)

set -euo pipefail

BIN_DIR="${HOME}/.local/bin"
BIN="${BIN_DIR}/pasture"
UNIT_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/systemd/user"
UNIT="${UNIT_DIR}/pasture.service"
ENV_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/pasture"
ENV_FILE="${ENV_DIR}/pasture.env"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "${HERE}/../.." && pwd)"
MODULE="github.com/nontechno/goat"

linger=0 restart=0 uninstall=0 purge=0 dry=0 prebuilt=""

say()  { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
run()  { if ((dry)); then printf '    %q' "$@"; printf '\n'; else "$@"; fi; }

while (($#)); do
    case "$1" in
        --linger) linger=1 ;;
        --restart) restart=1 ;;
        --uninstall) uninstall=1 ;;
        --purge) purge=1 ;;
        --dry-run) dry=1 ;;
        --bin) shift; prebuilt="${1:?--bin needs a file}" ;;
        -h|--help) sed -n '2,19p' "$0"; exit 0 ;;
        *) die "unknown option: $1 (see --help)" ;;
    esac
    shift
done

# ---- checks ------------------------------------------------------------------

[[ "$(uname -s)" == Linux ]] || die "systemd services are Linux only (macOS: see com.nontechno.pasture.plist)"
((EUID != 0)) || die "run this as your normal user, not root: pasture must run as the user whose programs it holds"
command -v systemctl >/dev/null || die "systemctl not found: this system does not use systemd"

if ! systemctl --user show-environment >/dev/null 2>&1; then
    msg="cannot reach your systemd user manager (systemctl --user)."
    if grep -qi microsoft /proc/version 2>/dev/null; then
        msg+=$'\n  This is WSL: enable systemd in /etc/wsl.conf ([boot] systemd=true),\n  run "wsl --shutdown" in PowerShell, reopen WSL, and try again (SYSTEMD.md, "WSL").'
    elif [[ -z "${XDG_RUNTIME_DIR:-}" ]]; then
        msg+=$'\n  XDG_RUNTIME_DIR is not set (e.g. after "su"/"sudo -u"). Log in as this user directly\n  (ssh or a desktop session), or: export XDG_RUNTIME_DIR=/run/user/$(id -u)'
    fi
    die "$msg"
fi

socket_path() {
    local dir="/tmp"
    if [[ -r "$ENV_FILE" ]]; then
        local v
        v="$(sed -n 's/^PASTURE_TMPDIR=//p' "$ENV_FILE" | tail -n1)"
        [[ -n "$v" ]] && dir="$v"
    fi
    printf '%s/pasture-%s/default' "$dir" "$(id -u)"
}

# ---- uninstall ---------------------------------------------------------------

if ((uninstall)); then
    if systemctl --user is-active --quiet pasture.service; then
        warn "stopping pasture: every program running on it ends now"
    fi
    run systemctl --user disable --now pasture.service 2>/dev/null || true
    run rm -f "$UNIT"
    run systemctl --user daemon-reload
    run systemctl --user reset-failed pasture.service 2>/dev/null || true
    if ((purge)); then
        sock="$(socket_path)"
        run rm -f "$BIN" "$ENV_FILE"
        run rmdir "$ENV_DIR" 2>/dev/null || true
        run rm -f "$(dirname "$sock")/pasture.log"
    fi
    say "pasture service removed$( ((purge)) && echo ', with binary, settings and log' )"
    exit 0
fi

# ---- build -------------------------------------------------------------------

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [[ -n "$prebuilt" ]]; then
    [[ -x "$prebuilt" ]] || die "$prebuilt is not an executable file"
    cp "$prebuilt" "$tmp/pasture"
else
    command -v go >/dev/null || die "go not found in PATH (install Go 1.24+, or use --bin FILE)"
    grep -q "^module ${MODULE}\$" "${REPO}/go.mod" 2>/dev/null ||
        die "expected the goat repository at ${REPO} (module ${MODULE})"
    say "building pasture from ${REPO}"
    if ((dry)); then
        run go build -trimpath -o "$tmp/pasture" ./pasture/cmd/pasture
        : >"$tmp/pasture"; chmod +x "$tmp/pasture"
    else
        (cd "$REPO" && go build -trimpath -o "$tmp/pasture" ./pasture/cmd/pasture)
    fi
fi
((dry)) || "$tmp/pasture" -version >/dev/null || die "the built binary does not run"

# ---- install -----------------------------------------------------------------

was_active=0
systemctl --user is-active --quiet pasture.service && was_active=1

say "installing ${BIN}"
run mkdir -p "$BIN_DIR"
# Install next to the target, then rename: atomic, and safe while the old
# binary is running.
run install -m 0755 "$tmp/pasture" "${BIN}.new"
run mv -f "${BIN}.new" "$BIN"

say "installing ${UNIT}"
run mkdir -p "$UNIT_DIR"
run install -m 0644 "${HERE}/pasture.service" "$UNIT"

if [[ -e "$ENV_FILE" ]]; then
    say "keeping existing settings ${ENV_FILE}"
else
    say "installing settings ${ENV_FILE}"
    run mkdir -p "$ENV_DIR"
    run install -m 0600 "${HERE}/pasture.env" "$ENV_FILE"
fi

run systemctl --user daemon-reload
run systemctl --user enable pasture.service

if ((was_active)); then
    if ((restart)); then
        warn "restarting pasture: every program running on it ends now"
        run systemctl --user restart pasture.service
    else
        say "pasture is already running; the new binary is used from its next start"
        say "  (restart now with --restart, or: systemctl --user restart pasture — ends its programs)"
    fi
else
    say "starting pasture"
    run systemctl --user start pasture.service
fi

if ((linger)); then
    say "enabling linger for ${USER} (service keeps running after logout)"
    run loginctl enable-linger "$USER" ||
        warn "could not enable linger; run: sudo loginctl enable-linger $USER"
fi

# ---- verify ------------------------------------------------------------------

((dry)) && exit 0
sock="$(socket_path)"
for _ in $(seq 50); do
    [[ -S "$sock" ]] && break
    sleep 0.1
done
if [[ -S "$sock" ]]; then
    say "pasture is running: pid $(cat "${sock}.lock" 2>/dev/null || echo '?'), socket ${sock}"
else
    systemctl --user --no-pager --lines=20 status pasture.service || true
    die "the socket ${sock} did not appear; see: journalctl --user -u pasture"
fi

case ":${PATH}:" in
    *":${BIN_DIR}:"*) ;;
    *) warn "${BIN_DIR} is not in your PATH (goat may need it to find pasture): add it to your shell profile" ;;
esac
if [[ "$(loginctl show-user "$USER" --property=Linger --value 2>/dev/null)" != yes ]]; then
    say "note: without linger, pasture stops when your last session ends (use --linger to keep it)"
fi
say "logs: journalctl --user -u pasture -f"
