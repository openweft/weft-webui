#!/usr/bin/env -S pkgx bash
# weft-webui-restore.sh — replay a tarball produced by
# weft-webui-backup.sh into /var/lib/weft-webui/.
#
# Usage :
#   sudo weft-webui-restore.sh <backup.tar.gz>          # restore
#   sudo weft-webui-restore.sh --dry-run <backup.tar.gz> # list only
#
# The restore is destructive : every persistence path the backup
# captured overwrites the running file in-place. The script :
#   1. unpacks the archive to a temp directory.
#   2. reads MANIFEST + compares the saved hostname to the running
#      one ; mismatch is a HARD fail unless --force is passed.
#   3. moves each captured file into place ; stops weft-webui
#      first so a restart isn't racing the JSON re-hydration.
#
# Pair with weft-webui-backup.sh (deploy/scripts/) — same envs,
# same on-disk layout.

set -euo pipefail

DRY_RUN=0
FORCE=0
ARCHIVE=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --force)   FORCE=1 ;;
    -h|--help)
      printf 'usage : %s [--dry-run] [--force] <backup.tar.gz>\n' "$0" >&2
      exit 0 ;;
    *)
      if [[ -n "$ARCHIVE" ]]; then
        printf '%s : only one archive accepted, got %q and %q\n' "$0" "$ARCHIVE" "$arg" >&2
        exit 1
      fi
      ARCHIVE="$arg" ;;
  esac
done

if [[ -z "$ARCHIVE" ]]; then
  printf 'usage : %s [--dry-run] [--force] <backup.tar.gz>\n' "$0" >&2
  exit 1
fi
if [[ ! -f "$ARCHIVE" ]]; then
  printf '%s : %q not found\n' "$0" "$ARCHIVE" >&2
  exit 1
fi

STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT

tar --extract --gzip --file="$ARCHIVE" --directory="$STAGING"

if [[ ! -f "$STAGING/MANIFEST" ]]; then
  printf '%s : %q has no MANIFEST — refusing to restore (use --force to override)\n' "$0" "$ARCHIVE" >&2
  if [[ "$FORCE" -ne 1 ]]; then exit 1; fi
fi

# Hostname guard : weft-webui state files carry session keys / audit
# trails the operator would never want to cross-pollinate between
# clusters. --force overrides for a deliberate cross-cluster restore.
if [[ -f "$STAGING/MANIFEST" ]]; then
  SAVED_HOST=$(awk -F': *' '/^hostname/ {print $2}' "$STAGING/MANIFEST")
  RUNNING_HOST=$(hostname -f 2>/dev/null || hostname)
  if [[ -n "$SAVED_HOST" && "$SAVED_HOST" != "$RUNNING_HOST" ]]; then
    if [[ "$FORCE" -ne 1 ]]; then
      printf '%s : MANIFEST hostname %q != running %q — refusing (pass --force to allow)\n' \
        "$0" "$SAVED_HOST" "$RUNNING_HOST" >&2
      exit 1
    fi
    printf '%s : MANIFEST hostname %q != running %q — overridden by --force\n' \
      "$0" "$SAVED_HOST" "$RUNNING_HOST" >&2
  fi
fi

# Walk every captured file (skip MANIFEST itself). The staging tree
# mirrors the absolute paths the backup recorded, so a file at
# $STAGING/var/lib/weft-webui/inventory.json lands at
# /var/lib/weft-webui/inventory.json.
walk_files() {
  find "$STAGING" -type f ! -name MANIFEST -print
}

if [[ "$DRY_RUN" -eq 1 ]]; then
  printf 'weft-webui-restore (dry-run) — would restore :\n'
  while IFS= read -r f; do
    rel="${f#$STAGING}"
    printf '  %s\n' "$rel"
  done < <(walk_files)
  exit 0
fi

# Stop the daemon so the JSON files settle before the next start.
# If systemd isn't present (container deploys), the operator runs
# the script while the binary is offline ; warn but continue.
if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet weft-webui; then
    systemctl stop weft-webui
    RESTART_AFTER=1
  fi
fi

while IFS= read -r f; do
  rel="${f#$STAGING}"
  mkdir -p "$(dirname "$rel")"
  cp -p "$f" "$rel"
  printf 'restored %s\n' "$rel"
done < <(walk_files)

if [[ "${RESTART_AFTER:-0}" -eq 1 ]]; then
  systemctl start weft-webui
  printf 'weft-webui-restore : daemon restarted\n'
fi
