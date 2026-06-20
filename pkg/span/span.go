// Package span defines the source-position primitive shared across the
// Pho toolchain: the parse AST (pkg/ast), the diagnostic model (pkg/diag),
// and the runtime's positioned-node wrapper (pkg/core) all describe source
// ranges with the same Span. It is a leaf package — it imports nothing from
// pho/* — so any layer can depend on it without pulling in the runtime.
package span

// Span is a half-open source range. Lines and columns are 1-based; EndCol
// points one past the last column of the range.
type Span struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
}
