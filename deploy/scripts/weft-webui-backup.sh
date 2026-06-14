#!/usr/bin/env -S pkgx bash
# weft-webui-backup.sh — bundle every persistence path + its rotation
# history into one tar.gz an operator can drop into S3 / Restic / etc.
#
# Reads the env from /etc/default/weft-webui by default so the same
# variables that drive the daemon decide what to back up. Each path
# is optional ; the script silently skips paths that aren't set.
#
#   sudo /usr/local/bin/weft-webui-backup.sh /var/backups/weft-webui-$(date +%F).tar.gz
#
# Pairs with the recovery recipe in deploy/README.md : un-tar into
# /var/lib/weft-webui/, restart the daemon, and the dashboard
# rehydrates the in-memory state from the JSON snapshots.

set -euo pipefail

ENV_FILE="${WEFT_WEBUI_ENV:-/etc/default/weft-webui}"
OUTPUT="${1:-}"
if [[ -z "$OUTPUT" ]]; then
  printf 'usage : %s <output.tar.gz>\n' "$0" >&2
  exit 1
fi

# Read the configured paths from the env file when it exists. The
# defaults match deploy/systemd/weft-webui.service so a bare-systemd
# install backs up the right files even without /etc/default override.
if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  set -a; source "$ENV_FILE"; set +a
fi

INVENTORY_PATH="${WEBUI_INVENTORY_PATH:-/var/lib/weft-webui/inventory.json}"
DNS_PATH="${WEBUI_DNS_PATH:-/var/lib/weft-webui/dns.json}"
SECURITY_PATH="${WEBUI_SECURITY_PATH:-/var/lib/weft-webui/security.json}"
SCRIPTS_PATH="${WEBUI_SCRIPTS_PATH:-/var/lib/weft-webui/scripts.json}"
AUDIT_PATH="${WEBUI_AUDIT_LOG_PATH:-/var/lib/weft-webui/audit.log}"

# Collect existing paths + their sibling .history/ dirs.
STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT

# strip the leading slash so $STAGING/$rel writes inside the staging
# dir even when $p is an absolute path. Works on both macOS (BSD)
# and Linux without relying on realpath's --relative-to flag.
relpath() {
  printf '%s\n' "${1#/}"
}

add() {
  local p="$1" rel
  if [[ -e "$p" ]]; then
    rel="$(relpath "$p")"
    mkdir -p "$STAGING/$(dirname "$rel")"
    cp -p "$p" "$STAGING/$rel"
  fi
  if [[ -d "${p}.history" ]]; then
    rel="$(relpath "${p}.history")"
    mkdir -p "$STAGING/$rel"
    cp -Rp "${p}.history/." "$STAGING/$rel/"
  fi
}

add "$INVENTORY_PATH"
add "$DNS_PATH"
add "$SECURITY_PATH"
add "$SCRIPTS_PATH"
add "$AUDIT_PATH"

# Stamp a manifest so the operator restoring later knows what was
# captured + when. The hostname + version land in metadata so a
# cross-cluster restore doesn't accidentally drop tenant-A's state
# onto tenant-B's box.
cat > "$STAGING/MANIFEST" <<EOF
created_at  : $(date -u +%Y-%m-%dT%H:%M:%SZ)
hostname    : $(hostname -f 2>/dev/null || hostname)
inventory   : $INVENTORY_PATH
dns         : $DNS_PATH
security    : $SECURITY_PATH
scripts     : $SCRIPTS_PATH
audit       : $AUDIT_PATH
EOF

# Build the archive. --auto-compress picks gzip from the .tar.gz
# suffix ; we use the GNU long-options form so this works under
# bash 5.x but doesn't depend on /usr/bin/tar quirks.
tar --create --gzip --file="$OUTPUT" \
    --directory="$STAGING" \
    --owner=0 --group=0 .

printf 'weft-webui-backup : wrote %s (%s)\n' \
  "$OUTPUT" "$(du -h "$OUTPUT" | cut -f1)"
