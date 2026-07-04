package lint

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// A call to a `!`-function makes its caller environmental — the caller must
// itself end in '!'. Environmental primitives enter only through the stdlib's
// `!`-named wrappers, so a `!`-call is the sole environmental source (there is
// no primitive table — freeform tracking).
func TestEffectCallRequiresBang(t *testing.T) {
	enableEffectCheck(t)
	sink := "(fun sink! (Number) None)\n(let sink! (n) = none)\n"
	bad := analyze(t, sink+"(fun beep (Number) None)\n(let beep (n) = (sink! n))\n")
	if !hasDiagWithName(bad, "missing-bang", "beep") {
		t.Fatalf("beep calls a `!`-function and must be flagged, got %#v", bad)
	}
	good := analyze(t, sink+"(fun beep! (Number) None)\n(let beep! (n) = (sink! n))\n")
	if hasDiag(good, "missing-bang") {
		t.Fatalf("beep! correctly marks its effect — clean, got %#v", good)
	}
}

// A `!`-named function with no visible inner effect is NOT flagged: a `!` name is
// trusted as effectful BY DECLARATION (freeform), so there is no spurious-bang.
func TestBangTrustedNoSpurious(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, "(fun nope! () Number)\n(let nope! () = 42)\n")
	if hasDiag(d, "spurious-bang") {
		t.Fatalf("a `!` name is effectful by declaration — no spurious-bang, got %#v", d)
	}
}

// `(var …)` is SIGNATURE-only: an IMPLEMENTATION names its receiver plainly
// `self` and carries no `(var …)` — neither a `(var self)` receiver nor a
// `(var out)` value param. Both draw var-in-impl.
func TestVarInImplForbidden(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, "(struct Box v)\n(method Box.set= ((var Self) Number) Box)\n(let Box.set= ((var self) x) = (= self.v x))\n")
	if !hasDiag(d, "var-in-impl") {
		t.Fatalf("(var self) in an impl → var-in-impl expected, got %#v", d)
	}
	d = analyze(t, "(struct Box v)\n(method Box.set= (Self Number) Box)\n(let Box.set= (self (var out)) = (= self.v out))\n")
	if !hasDiag(d, "var-in-impl") {
		t.Fatalf("(var out) value param in an impl → var-in-impl expected, got %#v", d)
	}
	good := analyze(t, "(struct Box v)\n(method Box.set= ((var Self) Number) Box)\n(let Box.set= (self x) = (= self.v x))\n")
	if hasDiag(good, "var-in-impl") {
		t.Fatalf("plain-self impl → no var-in-impl, got %#v", good)
	}
}

// A signature's `(var …)` must be the receiver `(var Self)`; a value parameter
// type cannot be mutable.
func TestVarSigReceiverOnly(t *testing.T) {
	enableEffectCheck(t)
	d := analyze(t, "(struct Box v)\n(method Box.set= ((var Self) (var Number)) Box)\n(let Box.set= (self x) = (= self.v x))\n")
	if !hasDiag(d, "var-non-receiver") {
		t.Fatalf("(var Number) sig param → var-non-receiver expected, got %#v", d)
	}
}

// Rule 1: a `(var Self)` receiver declares self-mutation, so the method NAME
// must carry the `=` suffix.
func TestVarSelfNeedsEquals(t *testing.T) {
	enableEffectCheck(t)
	bad := analyze(t, "(struct Box v)\n(method Box.bump ((var Self)) Box)\n(let Box.bump (self) = self)\n")
	if !hasDiag(bad, "var-self-needs-equals") {
		t.Fatalf("(var Self) receiver without '=' → var-self-needs-equals expected, got %#v", bad)
	}
	good := analyze(t, "(struct Box v)\n(method Box.bump= ((var Self)) Box)\n(let Box.bump= (self) = (= self.v 1))\n")
	if hasDiag(good, "var-self-needs-equals") {
		t.Fatalf("(var Self) + '=' → clean, got %#v", good)
	}
}

// Randomness enters through a `!`-named stdlib wrapper (`random/int!`); calling
// it makes the caller environmental, exactly like any other `!`-call.
func TestEffectRandomThroughBang(t *testing.T) {
	enableEffectCheck(t)
	bad := analyze(t, "(fun pick (Number) Number)\n(let pick (n) = (random/int! 0 n))\n")
	if !hasDiagWithName(bad, "missing-bang", "pick") {
		t.Fatalf("pick calls random/int! — missing-bang expected, got %#v", bad)
	}
	good := analyze(t, "(fun pick! (Number) Number)\n(let pick! (n) = (random/int! 0 n))\n")
	if hasDiag(good, "missing-bang") {
		t.Fatalf("pick! correctly marks its randomness — clean, got %#v", good)
	}
}

// Writing a module-level var is a mutates-free effect (a positive effect now,
// not just a spurious-bang suppressor).
func TestEffectMutatesFree(t *testing.T) {
	enableEffectCheck(t)
	bad := analyze(t, "(let var counter = 0)\n(let tick () = (= counter 1))\n")
	if !hasDiagWithName(bad, "missing-bang", "tick") {
		t.Fatalf("tick writes module var counter — missing-bang expected, got %#v", bad)
	}
	good := analyze(t, "(let var counter = 0)\n(let tick! () = (= counter 1))\n")
	if hasDiag(good, "missing-bang") || hasDiag(good, "spurious-bang") {
		t.Fatalf("tick! correctly marks its mutates-free — clean, got %#v", good)
	}
}

// A local that shadows a module var is a locally-owned (pure) write — the shadow
// tracking must NOT misclassify it as mutates-free.
func TestEffectMutatesFreeShadowIsPure(t *testing.T) {
	enableEffectCheck(t)
	shadowLocal := analyze(t, "(let var counter = 0)\n(fun calc () do (let var counter = 5) (= counter 6))\n")
	if hasDiag(shadowLocal, "missing-bang") {
		t.Fatalf("calc mutates a local `counter` shadowing the module var — must stay pure, got %#v", shadowLocal)
	}
	shadowParam := analyze(t, "(let var counter = 0)\n(let calc (counter) = (= counter 6))\n")
	if hasDiag(shadowParam, "missing-bang") {
		t.Fatalf("calc mutates its param `counter` shadowing the module var — must stay pure, got %#v", shadowParam)
	}
}

// StdlibEffectClean is a standing regression guard: with the checker on, the
// whole shipped script tree must draw ZERO effect diagnostics. Since effects are
// freeform (no primitive table), this fails when a function inherits an effect —
// calls a `!`-function or writes module state, or mutates self — without the
// matching `!`/`=` on its name.
func TestStdlibEffectClean(t *testing.T) {
	enableEffectCheck(t)
	var files []string
	filepath.Walk("../../script", func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && (filepath.Ext(p) == ".phl" || filepath.Ext(p) == ".pho") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, d := range AnalyzeFile(f, src) {
			switch d.Code {
			case "missing-bang", "missing-equals", "effect-through-readonly", "effect-in-pure-context", "guard-effect":
				t.Errorf("%s:%d [%s] %s", f, d.Span.StartLine, d.Code, d.Message)
			}
		}
	}
}
