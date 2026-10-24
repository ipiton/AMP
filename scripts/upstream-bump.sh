#!/usr/bin/env bash
# Bumps the upstream Alertmanager module in go-app/go.mod (docs/UPSTREAM_UPGRADE.md):
#
#   ./scripts/upstream-bump.sh <version> [--allow-fresh=<reason>]
#   ./scripts/upstream-bump.sh --diff <old> <new>
#
# The bump refuses any module it moves -- the upstream release itself and
# every require that `go get` + `go mod tidy` change -- published less than
# UPSTREAM_BUMP_MIN_AGE_DAYS (default 7) days ago, and puts go.mod and
# go.sum back. --allow-fresh=<reason> is the owner's exception; the reason
# is printed so it ends up in the commit message.
#
# --diff writes a unified diff of the upstream files AMP carries ports of
# between two releases and prints its path. A bump runs it too.
# Bash 3.2-compatible, like release-gate.sh.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GO_APP_DIR="$ROOT_DIR/go-app"
UPSTREAM_MODULE="github.com/prometheus/alertmanager"
MIN_AGE_DAYS="${UPSTREAM_BUMP_MIN_AGE_DAYS:-7}"
# Upstream files with ports in go-app: internal/upstreamcore and
# cmd/amp-upstream (app/*.go), internal/business/templating (template.go),
# the matcher parsers in routing and configvalidator (pkg/labels/parse.go).
PORTED_FILES="app/app.go app/reloader.go app/metrics.go app/lifecycle.go app/url.go template/template.go pkg/labels/parse.go"

log() { printf '[upstream-bump] %s\n' "$1"; }
die() { log "$1"; exit 1; }

usage() {
  sed -n '4,5p' "${BASH_SOURCE[0]}" | sed 's/^# *//'
  exit 2
}

# Prints "<path>@<version>" for each require in go.mod.
requires() {
  ( cd "$GO_APP_DIR" && go mod edit -json ) | jq -r '.Require[] | .Path + "@" + .Version' | sort
}

# Prints the age in whole days of a module version, from the proxy's
# publish time.
age_days() {
  local published
  published="$(cd "$GO_APP_DIR" && go list -m -json "$1" | jq -r '.Time // empty')" || return 1
  [[ -n "$published" ]] || return 1
  jq -nr --arg t "$published" '(now - ($t | sub("\\.[0-9]+"; "") | fromdateiso8601)) / 86400 | floor'
}

write_diff() {
  local old="$1" new="$2" old_dir new_dir out f
  old_dir="$(cd "$GO_APP_DIR" && go mod download -json "$UPSTREAM_MODULE@$old" | jq -r '.Dir // empty')"
  new_dir="$(cd "$GO_APP_DIR" && go mod download -json "$UPSTREAM_MODULE@$new" | jq -r '.Dir // empty')"
  [[ -n "$old_dir" && -n "$new_dir" ]] || die "cannot download $UPSTREAM_MODULE@$old or @$new"
  out="${TMPDIR:-/tmp}"; out="${out%/}/alertmanager-ported-$old-$new.diff"
  : >"$out"
  for f in $PORTED_FILES; do
    # -N: a file added or removed between releases shows up whole.
    diff -uN --label "$old/$f" --label "$new/$f" "$old_dir/$f" "$new_dir/$f" >>"$out"
  done
  if [[ -s "$out" ]]; then
    log "ported files changed between $old and $new: $out"
  else
    log "ported files are identical between $old and $new"
  fi
}

if [[ $# -ge 1 && "$1" == "--diff" ]]; then
  [[ $# -eq 3 ]] || usage
  write_diff "$2" "$3"
  exit 0
fi

[[ $# -ge 1 && $# -le 2 ]] || usage
NEW="$1"
ALLOW_FRESH=""
if [[ $# -eq 2 ]]; then
  case "$2" in
    --allow-fresh=?*) ALLOW_FRESH="${2#--allow-fresh=}" ;;
    *) usage ;;
  esac
fi

cd "$ROOT_DIR" || exit 1
if ! git diff --quiet HEAD -- go-app/go.mod go-app/go.sum; then
  die "go-app/go.mod or go.sum has uncommitted changes; commit or stash them first"
fi

OLD="$(cd "$GO_APP_DIR" && go list -m -f '{{.Version}}' "$UPSTREAM_MODULE")" || exit 1
if [[ "$OLD" == "$NEW" ]]; then
  log "$UPSTREAM_MODULE is already at $NEW"
  exit 0
fi
log "bumping $UPSTREAM_MODULE $OLD -> $NEW"

WORK_DIR="$(mktemp -d)"
trap 'rm -r "$WORK_DIR"' EXIT
cp "$GO_APP_DIR/go.mod" "$GO_APP_DIR/go.sum" "$WORK_DIR/"
put_back() {
  cp "$WORK_DIR/go.mod" "$WORK_DIR/go.sum" "$GO_APP_DIR/"
  log "go.mod and go.sum put back"
}

requires >"$WORK_DIR/before"
if ! ( cd "$GO_APP_DIR" && go get "$UPSTREAM_MODULE@$NEW" && go mod tidy ); then
  put_back
  die "go get / go mod tidy failed"
fi
requires >"$WORK_DIR/after"

fresh=""
moved=0
while IFS= read -r mv; do
  moved=$((moved + 1))
  if ! days="$(age_days "$mv")"; then
    fresh="$fresh  $mv (publish time unknown)"$'\n'
  elif (( days < MIN_AGE_DAYS )); then
    fresh="$fresh  $mv ($days days old)"$'\n'
  fi
done < <(comm -13 "$WORK_DIR/before" "$WORK_DIR/after")
log "$moved requires moved"

if [[ -n "$fresh" ]]; then
  log "younger than $MIN_AGE_DAYS days (supply-chain quarantine):"
  printf '%s' "$fresh"
  if [[ -z "$ALLOW_FRESH" ]]; then
    put_back
    die "refused; wait out the quarantine or rerun with --allow-fresh=<reason>"
  fi
  log "kept under --allow-fresh: $ALLOW_FRESH -- record it in the commit message"
fi

if git diff HEAD -- go-app/go.mod | grep -qE '^[-+](go|toolchain) '; then
  log "go.mod changed its go/toolchain line: bump CI and the image builders with it (docs/CI.md, Go Version)"
fi

write_diff "$OLD" "$NEW"
cat <<NEXT
[upstream-bump] next:
  1. carry the diff over into the ports (internal/upstreamcore, cmd/amp-upstream,
     internal/business/templating, the matcher parsers) and set
     upstreamcore.PortedFrom = "$NEW"
  2. cd go-app && go build ./... && go test ./... -count=1
  3. ./scripts/upstream-core-gate.sh -- review every baseline change it asks for
  4. cd go-app && go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
  5. ./scripts/release-gate.sh
NEXT
