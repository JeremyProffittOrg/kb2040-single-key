#!/usr/bin/env bash
# Copies the firmware in src/ onto an attached KB2040 running CircuitPython.
#
# The macOS and Linux counterpart of scripts/flash.ps1. Finds the CIRCUITPY volume, installs
# the CircuitPython libraries the firmware needs with circup, then mirrors src/ onto it.
#
# This is not a "deploy" in the sense deploy.md forbids: it writes to a USB mass-storage
# volume plugged into this machine, not to a cloud account. Nothing here touches AWS and
# nothing here needs a credential.
#
# Usage:
#   scripts/flash.sh                        # find CIRCUITPY automatically
#   scripts/flash.sh --drive /Volumes/CIRCUITPY
#   scripts/flash.sh --skip-libraries       # source only, libraries already installed
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_ROOT="$REPO_ROOT/src"

DRIVE=""
SKIP_LIBRARIES=0

while [ $# -gt 0 ]; do
  case "$1" in
    --drive) DRIVE="${2:-}"; shift 2 ;;
    --skip-libraries) SKIP_LIBRARIES=1; shift ;;
    -h|--help) sed -n '2,17p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

[ -d "$SRC_ROOT" ] || { echo "No src/ directory at $SRC_ROOT" >&2; exit 1; }

# --- locate the board --------------------------------------------------------------------

find_circuitpy() {
  # macOS mounts removable volumes under /Volumes; Linux desktops use /media/$USER or /run/
  # media/$USER, and a manual mount is commonly /mnt.
  local candidate
  for candidate in \
      /Volumes/CIRCUITPY \
      "/media/$USER/CIRCUITPY" \
      "/run/media/$USER/CIRCUITPY" \
      /media/CIRCUITPY \
      /mnt/CIRCUITPY; do
    [ -d "$candidate" ] && { printf '%s' "$candidate"; return 0; }
  done
  return 1
}

if [ -z "$DRIVE" ]; then
  DRIVE="$(find_circuitpy)" || {
    cat >&2 <<'EOF'
No CIRCUITPY volume found.

  * Is the board plugged in?
  * Does it have CircuitPython on it? A board in bootloader mode mounts as RPI-RP2
    instead; copy a CircuitPython .uf2 onto that first.
    https://circuitpython.org/board/adafruit_kb2040/
  * Mounted somewhere unusual? Pass it explicitly:
        scripts/flash.sh --drive /path/to/CIRCUITPY

On Linux the volume may not automount at all. Check `lsblk -o NAME,LABEL,MOUNTPOINT`
and mount it, or use a desktop file manager to open it once.
EOF
    exit 1
  }
fi

[ -d "$DRIVE" ] || { echo "Drive $DRIVE is not available." >&2; exit 1; }
[ -f "$DRIVE/boot_out.txt" ] || {
  echo "$DRIVE does not look like a CircuitPython drive (no boot_out.txt)." >&2
  exit 1
}

echo "Board on $DRIVE"
sed 's/^/  /' "$DRIVE/boot_out.txt"

# --- libraries ---------------------------------------------------------------------------

# Not vendored into the repo: circup installs the build matching the board's CircuitPython
# version, which is the thing that actually has to match.
LIBRARIES=(adafruit_hid neopixel adafruit_pixelbuf)

if [ "$SKIP_LIBRARIES" -eq 1 ]; then
  echo
  echo "Skipping library install (--skip-libraries)."
else
  command -v circup >/dev/null 2>&1 || {
    echo "circup is not installed. Run: pip install -r requirements-dev.txt" >&2
    exit 1
  }
  echo
  echo "Installing libraries: ${LIBRARIES[*]}"
  circup --path "$DRIVE" install "${LIBRARIES[@]}"
fi

# --- firmware ----------------------------------------------------------------------------

echo
echo "Copying firmware"

for file in boot.py code.py; do
  cp "$SRC_ROOT/$file" "$DRIVE/$file"
  echo "  $file"
done

# Remove first so a module deleted from the repo does not linger on the board and get
# imported in preference to what is actually in src/.
rm -rf "$DRIVE/singlekey"
cp -R "$SRC_ROOT/singlekey" "$DRIVE/singlekey"
# CircuitPython does not use __pycache__, and copying one wastes space on a small drive.
rm -rf "$DRIVE/singlekey/__pycache__"
for f in "$DRIVE"/singlekey/*.py; do
  echo "  singlekey/$(basename "$f")"
done

# Flush to the device before reporting success; on Linux the copy may still be in page cache.
sync 2>/dev/null || true

cat <<'EOF'

Done.

boot.py only takes effect after a hard reset, so unplug and replug the board (or press
reset) before configuring it. You should then see two serial ports.

Next:
  go run ./cli/cmd/kb2040ctl ports
  go run ./cli/cmd/kb2040ctl info
EOF
