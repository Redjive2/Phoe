package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
)

// A signature with a proper TYPE return (`None` for nil-returning, `Boolean` for
// bool) must be recognized as a SIGNATURE (erased at runtime), so its (= …)
// implementation defines the name ONCE — not a redeclare. Regression: the runtime
// sig-detector (isFunSig, pkg/builtins/decl.go) must match the linter's
// (isFunSigForm, pkg/lint/decls.go); a mismatch makes a .phl lint clean but fail
// at load with "cannot declare function '…': name already in use". (The nil TYPE
// is `None`; the value `none` is NOT a type — see the value/type invariants.)
func TestSigImplReturnTypes(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pho-sitest")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, filepath.Join("testdata", "sig_impl_none.pho"))
	cmd.Env = append(cmd.Environ(), "NO_COLOR=1")
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("sig+impl with proper type returns must load clean; got:\n%s", errBuf.String())
	}
	if errBuf.Len() != 0 {
		t.Errorf("expected silent stderr; got:\n%s", errBuf.String())
	}
}
