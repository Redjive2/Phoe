package builtins

import (
	"pho/pkg/core"
	"testing"
)

func TestProbeMacroTwice(t *testing.T) {
	v := evalProgram(t, `(macro twice! (x) (pause (slice "+" x x)))
(twice! 5)`)
	t.Logf("twice!5 = kind=%s val=%v", v.Kind, v.Val)
}

func TestProbeMacroStringArg(t *testing.T) {
	// macro whose body returns a string literal as code
	v := evalProgram(t, `(macro sid! (x) (pause x))
(sid! "hello")`)
	t.Logf("sid! hello = kind=%s val=%v", v.Kind, v.Val)
}

func TestProbeResumeRaw(t *testing.T) {
	v := evalProgram(t, `(resume (slice "+" "1" "2"))`)
	t.Logf("resume = kind=%s val=%v", v.Kind, v.Val)
}

func TestProbeResumeEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("PANIC: %v", r)
		}
	}()
	v := evalProgram(t, `(resume (slice))`)
	t.Logf("resume empty = kind=%s val=%v", v.Kind, v.Val)
}

func TestProbeNumberKindStringify(t *testing.T) {
	_ = core.TvNil
}
