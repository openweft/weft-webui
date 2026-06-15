#!/usr/bin/env -S pkgx bash
# weft-webui-backup.sh — bundle every persistence path + its rotation
# history into one tar.gz that drops cleanly into a weft-block
# backup target (or any external store).
#
# CANONICAL DR : weft-block volume snapshots. This script is the
# escape hatch when you want a portable tarball decoupled from
# weft-block — e.g. seeding a fresh cluster from a peer, archiving
# to S3, or feeding into a borg / Restic store.
#
# Run INSIDE the weft-webui microVM via :
#
#   weft microvm exec weft-webui -- /usr/local/bin/weft-webui-backup.sh \
#       /var/lib/weft-webui/backup.tar.gz
#
# Then pull the file out with `weft microvm cp`.
#
# The script reads WEBUI_* env vars from the current shell ; the
# microVM already has them set from cluster.hcl's weft-webui block.

set -euo pipefail

OUTPUT="${1:-}"
if [[ -z "$OUTPUT" ]]; then
  printf 'usage : %s <output.tar.gz>\n' "$0" >&2
  exit 1
fi

INVENTORY_PATH="${WEBUI_INVENTORY_PATH:-/var/lib/weft-webui/inventory.json}"
DNS_PATH="${WEBUI_DNS_PATH:-/var/lib/weft-webui/dns.json}"
SECURITY_PATH="${WEBUI_SECURITY_PATH:-/var/lib/weft-webui/security.json}"
SCRIPTS_PATH="${WEBUI_SCRIPTS_PATH:-/var/lib/weft-webui/scripts.json}"
AUDIT_PATH="${WEBUI_AUDIT_LOG_PATH:-/var/lib/weft-webui/audit.log}"

STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT

# strip the leading slash so $STAGING/$rel writes inside the staging
# dir even when $p is an absolute path. Works on both BSD + GNU
# without realpath's --relative-to flag.
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

tar --create --gzip --file="$OUTPUT" \
    --directory="$STAGING" \
    --owner=0 --group=0 .

printf 'weft-webui-backup : wrote %s (%s)\n' \
  "$OUTPUT" "$(du -h "$OUTPUT" | cut -f1)"
