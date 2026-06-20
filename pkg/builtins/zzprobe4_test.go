package builtins

import "testing"

func runSafe(t *testing.T, name, src string) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("%s PANIC: %v", name, r)
		}
	}()
	v := evalProgram(t, src)
	t.Logf("%s = kind=%s val=%v", name, v.Kind, v.Val)
}

func TestProbeAdversarial(t *testing.T) {
	runSafe(t, "resume-num", `(resume 5)`)
	runSafe(t, "resume-nil", `(resume Nil)`)
	runSafe(t, "resume-str", `(resume "hello")`)
	runSafe(t, "resume-nested-empty", `(resume (slice (slice)))`)
	runSafe(t, "resume-bad-head", `(resume (slice "nosuchfn" "1"))`)
	runSafe(t, "macrocall-on-fun", `(fun f (x) x)
(f! 3)`)
	runSafe(t, "macrocall-on-num", `(const n 5)
(n! 3)`)
	runSafe(t, "pause-resume-rt", `(resume (pause (slice "+" 1 2)))`)
	runSafe(t, "pause-map", `(pause {"a" 1})`)
}
