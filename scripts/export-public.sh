#!/usr/bin/env bash
# Export the open-source tree of costhound: every tracked file except the paid
# code (Go files starting with //go:build pro), the fix-bundle scripts and
# their test (internal/fixplan, scripts/e2e-bundle.sh), the seller's license
# tool, the sales-site demos and the Pro license. Then prove the export
# builds, vets and passes its tests on its own, contains nothing paid, and
# keeps no paid logic in its free files (the two gates at the end).
#
# Usage: scripts/export-public.sh <empty or new directory>
# It never deletes anything: the target must not exist or must be empty.
set -euo pipefail
cd "$(dirname "$0")/.."

DEST=${1:?usage: $0 <empty or new directory>}
if [ -e "$DEST" ] && [ -n "$(ls -A "$DEST" 2>/dev/null)" ]; then
  echo "$DEST is not empty: pick a new directory (nothing was changed)" >&2
  exit 1
fi
untracked=$(git ls-files --others --exclude-standard -- '*.go' '*.html' go.mod go.sum)
if [ -n "$untracked" ]; then
  echo "these files aren't tracked by git yet, so the export would miss them (git add them first):" >&2
  echo "$untracked" >&2
  exit 1
fi
mkdir -p "$DEST"

kept=0 skipped=0
while IFS= read -r f; do
  case "$f" in
    cmd/costhound-license/*|internal/fixplan/*|scripts/e2e-bundle.sh|scripts/demo-report.go|scripts/demo-history.go|LICENSE-PRO|.gitleaksignore) skipped=$((skipped + 1)); continue ;;
    internal/report/report.html|internal/report/cloud_report.html|internal/report/summary_report.html|internal/dashboard/dashboard.html|internal/azure/testdata/*) skipped=$((skipped + 1)); continue ;; # assets used only by paid Go files
  esac
  if [[ "$f" == *.go ]]; then
    case "$(head -n 1 "$f")" in
      '//go:build pro') skipped=$((skipped + 1)); continue ;;
      '//go:build !pro') ;;
      //go:build*pro*)
        echo "$f: its build line mentions pro but isn't exactly //go:build pro or //go:build !pro, so the export can't tell which tree it belongs to (nothing was exported)" >&2
        exit 1 ;;
    esac
  fi
  mkdir -p "$DEST/$(dirname "$f")"
  if [[ "$f" == go.mod ]]; then
    # Drop paid module roots before tidy so they can't select versions for the free tree.
    sed -E '/\/\/ (indirect; )?paid-only$/d' "$f" > "$DEST/$f"
  else
    cp -p "$f" "$DEST/$f"
  fi
  kept=$((kept + 1))
done < <(git ls-files)
echo "exported $kept files to $DEST ($skipped paid or private files left out)"

cd "$DEST"
go mod tidy
go build ./...
go vet ./...
go test ./... > /dev/null
echo "public tree: builds, vets and passes its tests"

# Nothing paid may leak: no pro-tagged file, no seller tool, no private key material.
# (Each check captures its matches, then tests them: a grep -q at the end of
# a pipe can make its writer die of SIGPIPE on big output, and under pipefail
# the leak would then go unseen.)
leak=$(grep -rlx '//go:build pro' --include='*.go' . || true)
if [ -n "$leak" ]; then
  echo "LEAK: a pro-tagged file is in the export:" >&2; echo "$leak" >&2; exit 1
fi
leak=$(grep -rIl -e 'BEGIN .*PRIVATE KEY' -e 'costhound-seller/signing.key' . | grep -v '^./scripts/' || true)
if [ -n "$leak" ]; then
  echo "LEAK: private key material or seller paths found:" >&2; echo "$leak" >&2; exit 1
fi
if [ -e internal/fixplan ] || [ -e scripts/e2e-bundle.sh ]; then
  echo "LEAK: the paid fix-bundle scripts are in the export" >&2; exit 1
fi
go build -o /dev/null . && echo "public tree: the free binary builds"

# Paid logic stays in pro files. A function the free tree never reaches is
# one only the paid code calls: it belongs in a _pro.go file, behind a free
# stub if free code calls it (go tool deadcode, pinned in go.mod; run with
# and without -test, so a paid function a public test still calls is caught
# too). Allowed ones: "Name  reason", one per line.
DEADCODE_ALLOW='
'
dead=$( { go tool deadcode -test ./... && go tool deadcode ./...; } | awk -v allow="$(printf '%s\n' "$DEADCODE_ALLOW" | awk 'NF {print $1}')" '
  BEGIN { n = split(allow, a, "\n"); for (i = 1; i <= n; i++) ok[a[i]] = 1 }
  !($NF in ok)' | sort -u)
if [ -n "$dead" ]; then
  echo "LEAK: the free tree has functions only paid code reaches; move them to a _pro.go file (or allow one, with its reason):" >&2
  echo "$dead" >&2
  exit 1
fi
# And no exported method the free tree never calls. deadcode counts every
# exported method of a type that reaches reflection (encoding/json, fmt) as
# live, so report.Report's paid engines hid from it. A method counts as
# called when ".Name" appears in an exported non-test file. The ones the
# standard library calls through an interface are allowed: "Name  reason".
METHOD_ALLOW='
Set  flag.Value: the flag package calls it (fs.Var, the -tf-state flag)
'
leak=$( { grep -rhoE '^func \([A-Za-z_][A-Za-z0-9_]* \*?[A-Za-z_][A-Za-z0-9_]*(\[[^]]*\])?\) [A-Z][A-Za-z0-9_]*\(' --include='*.go' --exclude='*_test.go' . || true; } |
  sed -E 's/^func \([^)]*\) ([A-Z][A-Za-z0-9_]*)\($/\1/' | sort -u | while read -r m; do
    printf '%s\n' "$METHOD_ALLOW" | awk -v m="$m" '$1 == m {found = 1} END {exit !found}' && continue
    grep -rqE "\.${m}\b" --include='*.go' --exclude='*_test.go' . || echo "$m"
  done)
if [ -n "$leak" ]; then
  echo "LEAK: exported methods nothing in the free tree calls (the paid code's?); move them to a _pro.go file, or allow one with its reason:" >&2
  echo "$leak" >&2
  exit 1
fi
# And no free file touches the paid usage data, which only pro loaders fill:
# r.Usage or .Tail in any form (read, passed on, compared to nil), save the
# flag set's own Usage text.
leak=$(grep -rnE '\.(Usage|Tail)\b' --include='*.go' . | grep -v -e '_test\.go:' -e 'fs\.Usage = ' || true)
if [ -n "$leak" ]; then
  echo "LEAK: a free file touches the paid usage data (.Usage or .Tail); move that logic to a _pro.go file:" >&2
  echo "$leak" >&2
  exit 1
fi
# And no asset only paid code embeds: a non-Go file under internal/ must be
# named by an exported Go file (an embed, a test's testdata).
leak=$(find internal -type f ! -name '*.go' | while read -r a; do grep -rqF --include='*.go' "$(basename "$a")" . || echo "$a"; done)
if [ -n "$leak" ]; then
  echo "LEAK: assets no exported Go file uses (a paid feature's?); leave them out of the export:" >&2
  echo "$leak" >&2
  exit 1
fi
echo "public tree: no paid logic in its free files (deadcode, uncalled methods, usage data, assets)"
