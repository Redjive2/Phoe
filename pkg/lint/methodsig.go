package lint

import (
	"pho/pkg/annot"
	"pho/pkg/ast"
	"pho/pkg/core"
)

// Method-signature surface — the ObjectModel half of the "Sig surface"
// (Coordination §3). A `(method T.M …)` annotated with the method form of
// `--@ (~sig Recv (P…) R)` has its parameter/result types recorded onto the
// owner type's member surface (structInfo.MethodSigs); the gradual checker
// (typecheck.go) then reads them through methodSigFor to type a method call
// `x.M(args)`. The receiver type is omitted from the stored funSig — it is the
// type of `x`, checked at the call site, not an argument.
//
// Local methods are harvested here (harvestMethodSigs); imported methods'
// INLINE signatures are harvested by PackageStructs onto the imported struct's
// MethodSigs. Both are read at a call site through methodSigForShape, which
// resolves the receiver's owner across the import boundary via resolveStruct.
// (The legacy `--@ (~sig …)` annotation form is not harvested across imports —
// it is inert; inline signatures are the current mechanism.)

// harvestMethodSigs records every top-level method's ~sig signature onto its
// owner's structInfo, reusing the same memoized annotation evaluator the
// function-signature harvest uses.
func (w *walker) harvestMethodSigs(scope *Scope, tree []ast.PNode) {
	ensured := false
	env := collectTypeAliases(tree)
	for _, form := range tree {
		br, ok := form.(*ast.PBranch)
		if !ok || len(br.Annotations) == 0 {
			continue
		}
		d, ok := declOf(br)
		if !ok || d.Head != "method" || d.Owner == "" || d.Name == "" {
			continue
		}
		if !ensured {
			annot.EnsureDefault(resolveImportPath(w.file, "std/annot"))
			ensured = true
		}
		sig := methodSigFromEntries(harvestEntries(br), env)
		if sig == nil {
			continue
		}
		si, ok := scope.LookupStruct(d.Owner)
		if !ok {
			si = scope.structAt(d.Owner)
		}
		if si.MethodSigs == nil {
			si.MethodSigs = map[string]*funSig{}
		}
		si.MethodSigs[d.Name] = sig
	}

	// Inline method SIGNATURES populate the same MethodSigs surface
	// (TypeSignatures.md Phase 3). Processed after the annotation pass, and
	// only when absent, so a legacy `--@ (~sig …)` still wins if both exist.
	for _, form := range tree {
		d, ok := declOf(form)
		if !ok || !d.IsSig || d.Head != "method" || d.Owner == "" || d.Name == "" {
			continue
		}
		sig := inlineMethodSig(d, env)
		if sig == nil {
			continue
		}
		si, ok := scope.LookupStruct(d.Owner)
		if !ok {
			si = scope.structAt(d.Owner)
		}
		if si.MethodSigs == nil {
			si.MethodSigs = map[string]*funSig{}
		}
		if _, exists := si.MethodSigs[d.Name]; !exists {
			si.MethodSigs[d.Name] = sig
		}
	}
}

// methodSigFromEntries builds a funSig from the method form of a ~sig
// annotation's harvested entries (kind "sig", with a "receiver" plus
// "params"/"result"). Mirrors sigFromEntries; the receiver is recorded
// separately by the annotation and is not part of the call-argument signature
// (it is the type of `self`, checked at the call site). Returns nil when the
// entries carry no signature.
func methodSigFromEntries(entries []annot.Entry, env typeEnv) *funSig {
	var isSig bool
	var params, result core.Value
	for _, e := range entries {
		switch e.Key {
		case "kind":
			if s, _ := e.Value.Val.(string); s == "sig" {
				isSig = true
			}
		case "params":
			params = e.Value
		case "result":
			result = e.Value
		}
	}
	if !isSig {
		return nil
	}
	return exactSig(resolveTypeList(params, env), resolveAnnotType(result, env))
}

// methodSigFor returns the declared signature of method `member` on the LOCAL
// type `typeName`, or nil when none is in scope (un-annotated or unknown). Used
// while checking a method's own body, where the owner is a local declaration;
// call sites use methodSigForShape, which also crosses the import boundary.
func (w *walker) methodSigFor(scope *Scope, typeName, member string) *funSig {
	if si, ok := scope.LookupStruct(typeName); ok && si.MethodSigs != nil {
		if sig, found := si.MethodSigs[member]; found {
			return sig
		}
	}
	return nil
}

// methodSigForShape returns the declared signature of method `member` for a
// RECEIVER shape, crossing the import boundary: an imported struct instance
// (OwnerPkg set) reads its owner package's harvested MethodSigs, a local
// instance reads the scope. This is how a call `x.M(args)` type-checks its args
// regardless of where M's owner is declared. nil when the receiver's struct or
// the method signature is unknown (gradual — no check).
func (w *walker) methodSigForShape(scope *Scope, sh Shape, member string) *funSig {
	si, ok := w.resolveStruct(scope, sh)
	if !ok || si.MethodSigs == nil {
		return nil
	}
	return si.MethodSigs[member]
}
