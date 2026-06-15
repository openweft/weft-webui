#!/usr/bin/env -S pkgx bash
# weft-webui-restore.sh — replay a tarball produced by
# weft-webui-backup.sh into /var/lib/weft-webui/ from inside the
# microVM.
#
# CANONICAL DR : weft-block volume snapshots / restore. This
# script is the escape hatch when you have a tarball decoupled
# from weft-block — e.g. a backup pulled from S3, a borg restore,
# or seeding a fresh cluster from a peer's tarball.
#
# Run INSIDE the weft-webui microVM via :
#
#   weft microvm cp local-backup.tar.gz weft-webui:/tmp/
#   weft microvm exec weft-webui -- /usr/local/bin/weft-webui-restore.sh \
#       --dry-run /tmp/local-backup.tar.gz       # preview
#   weft microvm exec weft-webui -- /usr/local/bin/weft-webui-restore.sh \
#       /tmp/local-backup.tar.gz                  # apply
#
# The script unpacks + the MANIFEST hostname check refuses
# cross-cluster restores unless --force is passed. It does NOT
# restart the weft-webui process ; the caller drives that with :
#
#   weft microvm restart weft-webui

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

while IFS= read -r f; do
  rel="${f#$STAGING}"
  mkdir -p "$(dirname "$rel")"
  cp -p "$f" "$rel"
  printf 'restored %s\n' "$rel"
done < <(walk_files)

printf 'weft-webui-restore : done — `weft microvm restart weft-webui` to apply.\n'
