#!/usr/bin/env bash
# Comment-density regression guard (ADR 0041). Fails if any non-test Go
# package exceeds BUDGET percent comment lines. This is a ceiling against
# drift back toward AI-slop verbosity, NOT a quality target — the real
# bar is the per-comment WHY-not-WHAT rule. The budget sits above current
# density on purpose; the goal is catching a package ballooning to the
# 60-80% range the trim removed, not nitpicking well-documented code.
#
# Per-package (a directory) rather than per-file so a tight package
# doc-comment on a short file does not trip the guard. Packages below
# MIN_LINES of non-test code are exempt — the ratio is noise at that size.
set -euo pipefail

BUDGET="${COMMENT_BUDGET:-55}"
MIN_LINES="${COMMENT_MIN_LINES:-40}"

cd "$(git rev-parse --show-toplevel)"

fail=0
report=""
while read -r dir; do
	tot=0
	cmt=0
	while read -r f; do
		[ -z "$f" ] && continue
		tot=$((tot + $(wc -l <"$f")))
		cmt=$((cmt + $(grep -c '^[[:space:]]*//' "$f" || true)))
	done < <(git ls-files "$dir/*.go" | grep -v '_test\.go$' | awk -F/ -v d="$dir" '{p=$0; sub("/[^/]*$","",p); if (p==d) print}')

	[ "$tot" -lt "$MIN_LINES" ] && continue
	pct=$((cmt * 100 / tot))
	line=$(printf '%3d%%  %4d/%-4d  %s' "$pct" "$cmt" "$tot" "$dir")
	report="${report}${line}"$'\n'
	if [ "$pct" -gt "$BUDGET" ]; then
		report="${report}  ^^ exceeds ${BUDGET}% budget"$'\n'
		fail=1
	fi
done < <(git ls-files '*.go' | grep -v '_test\.go$' | sed 's#/[^/]*$##' | sort -u)

printf '%s' "$report" | sort -rn | head -20
echo "budget: ${BUDGET}% per package (packages under ${MIN_LINES} lines exempt)"

if [ "$fail" -ne 0 ]; then
	echo "FAIL: a package exceeds the comment-density budget — trim WHAT-restating comments (see ADR 0041)." >&2
	exit 1
fi
echo "OK: all packages within the comment-density budget."
