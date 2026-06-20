package core

import "pho/pkg/span"

// Span is re-exported from pkg/span as a convenience alias: the runtime
// uses spans pervasively (spanned.go, diag.go, context.go), and the alias
// lets those files — and existing core.Span consumers — keep the short
// name. The canonical definition lives in pkg/span, the leaf package the
// parse AST (pkg/ast) and diagnostics (pkg/diag) also build on.
type Span = span.Span
