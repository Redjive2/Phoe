#!/usr/bin/env bash
#
# split-migrate.sh — run the decl/impl-split codemod (`-split`) across the whole
# tree with the correct file-set and exclusions baked in. This is the MECHANICAL
# half of the flip (Doc/PlanV1/DeclImplSplit.md): it head-swaps `fun`/`method`
# IMPLEMENTATIONS to `(= …)` and unwraps old-form property delegates. It does NOT
# touch signatures.
#
# ⚠ Run this ONLY after Phase 0 has landed and you have taught the remaining
# literal-head consumers the `=` case (the 9-item checklist in DeclImplSplit.md).
# Running it before those land turns the suite red (see the "End-to-end
# validation" section).
#
# Usage:
#   tooling/snakecase/split-migrate.sh -n     # dry run — report edits, write nothing
#   tooling/snakecase/split-migrate.sh        # apply the migration
#
# Run from the repo root.
set -euo pipefail

DRY=""
if [[ "${1:-}" == "-n" ]]; then
	DRY="-n"
fi

if [[ ! -f go.mod ]]; then
	echo "error: run from the repo root (go.mod not found)" >&2
	exit 1
fi

BIN="$(mktemp -d)/snakecase"
echo "building codemod…"
go build -o "$BIN" ./tooling/snakecase/

# 1. Pho source: every .phl/.pho. The codemod REFUSES on lex/parse errors, so the
#    intentional parse-failure fixtures (testdata/badlib/bad.phl, testdata/parse_err.pho)
#    are skipped automatically — no explicit exclusion needed.
#    (Paths under these dirs contain no spaces, so word-splitting is safe; this
#    keeps the script portable to macOS's bash 3.2, which lacks `mapfile`.)
echo
echo "== .phl / .pho (Pho source + tree-sitter examples) =="
PHO_FILES=$(find script pkg/builtins/pho testdata tooling/tree-sitter-pho/examples \
	\( -name '*.phl' -o -name '*.pho' \) 2>/dev/null | sort)
# shellcheck disable=SC2086
"$BIN" -split $DRY $PHO_FILES

# 2. Pho embedded in Go string literals: every *_test.go EXCEPT two kinds of
#    NEGATIVE tests whose fixtures INTENTIONALLY hold OLD-form strings and would be
#    defeated by migrating them:
#      - tooling/snakecase/*_test.go — the codemod's own corpus (finding A).
#      - pkg/lint/property_form_test.go — asserts the OLD flat property form is
#        REJECTED; unwrapping its fixture to the new form removes the rejection.
#    NOTE: this exclusion list may grow at flip time — any future test that asserts
#    an OLD impl/property form is rejected (Phase-4 hardening) must be added here.
echo
echo "== _test.go (Pho embedded in Go strings; excludes negative-form tests) =="
GO_FILES=$(find . -name '*_test.go' \
	-not -path './tooling/snakecase/*' \
	-not -path './pkg/lint/property_form_test.go' 2>/dev/null | sort)
# shellcheck disable=SC2086
"$BIN" -split -go $DRY $GO_FILES

echo
if [[ -n "$DRY" ]]; then
	echo "DRY RUN complete — nothing written."
else
	echo "Migration applied. REMAINING MANUAL STEPS (see Doc/PlanV1/DeclImplSplit.md):"
	echo "  • Teach the 9 literal-head consumers the '=' case (typecheck.go, effects.go,"
	echo "    trait.go, checkers.go) — the 'Consumer audit' checklist."
	echo "  • Regenerate goldens (TestSemanticTokensGolden) and fix position/source-text"
	echo "    assertions (TestCLIDiagnostics carets/traces, reorder_test labels)."
	echo "  • Phase 4: intolerant hardening (old impl form → error, force IsSig),"
	echo "    jump-to-implementation LSP, tree-sitter queries."
	echo "  • Run: go test ./...   (expect green once the consumer fixes land)"
fi
