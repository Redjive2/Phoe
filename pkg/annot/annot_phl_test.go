package annot

import (
	"testing"

	"pho/pkg/core"
)

// harvestAnnotPhl loads the std/annot macro library via the production
// LoadMacros harvest and returns its overlay. The path is relative to this
// package's directory (go test's working dir).
func harvestAnnotPhl(t *testing.T) map[string]core.StackEntry {
	t.Helper()
	overlay, err := LoadMacros("../../script/std/annot")
	if err != nil {
		t.Fatalf("LoadMacros: %v", err)
	}
	return overlay
}

func entryString(t *testing.T, res Result, key string) string {
	t.Helper()
	for _, e := range res.Entries {
		if e.Key == key {
			s, ok := e.Value.Val.(string)
			if !ok {
				t.Fatalf("entry %q is not a string: %#v", key, e.Value)
			}
			return s
		}
	}
	t.Fatalf("no %q entry in %#v", key, res.Entries)
	return ""
}

func entryStrings(t *testing.T, res Result, key string) []string {
	t.Helper()
	for _, e := range res.Entries {
		if e.Key == key {
			ptr, ok := e.Value.Val.(*[]core.Value)
			if !ok {
				t.Fatalf("entry %q is not an array: %#v", key, e.Value)
			}
			out := make([]string, len(*ptr))
			for i, v := range *ptr {
				out[i], _ = v.Val.(string)
			}
			return out
		}
	}
	t.Fatalf("no %q entry in %#v", key, res.Entries)
	return nil
}

func diagMsgs(res Result) []string {
	out := make([]string, len(res.Diags))
	for i, d := range res.Diags {
		out[i] = d.Message
	}
	return out
}

func entryValue(t *testing.T, res Result, key string) core.Value {
	t.Helper()
	for _, e := range res.Entries {
		if e.Key == key {
			return e.Value
		}
	}
	t.Fatalf("no %q entry in %#v", key, res.Entries)
	return core.Value{}
}

func dictGet(t *testing.T, v core.Value, field string) core.Value {
	t.Helper()
	m, ok := v.Val.(*map[core.Value]core.Value)
	if !ok {
		t.Fatalf("value is not a dict: %#v", v)
	}
	for k, val := range *m {
		if s, _ := k.Val.(string); s == field {
			return val
		}
	}
	t.Fatalf("dict has no field %q: %#v", field, v)
	return core.Value{}
}

func dictString(t *testing.T, v core.Value, field string) string {
	t.Helper()
	s, ok := dictGet(t, v, field).Val.(string)
	if !ok {
		t.Fatalf("dict field %q is not a string", field)
	}
	return s
}

func dictStrings(t *testing.T, v core.Value, field string) []string {
	t.Helper()
	ptr, ok := dictGet(t, v, field).Val.(*[]core.Value)
	if !ok {
		t.Fatalf("dict field %q is not an array", field)
	}
	out := make([]string, len(*ptr))
	for i, e := range *ptr {
		out[i], _ = e.Val.(string)
	}
	return out
}

// The real macros from annot.phl attach the expected metadata when driven
// through the isolated evaluator.
func TestAnnotPhlMacros(t *testing.T) {
	ev := New(harvestAnnotPhl(t))

	t.Run("type is inert (disconnected, Phase 4)", func(t *testing.T) {
		// ~type is disconnected (TypeSignatures.md Phase 4): the macro is kept in
		// annot.phl but harvests nothing — inline typed bindings carry the type now.
		res := ev.Evaluate(`(~type str)`, parseForm(t, `(~type str)`))
		if len(res.Diags) != 0 {
			t.Fatalf("unexpected diags: %v", diagMsgs(res))
		}
		if len(res.Entries) != 0 {
			t.Fatalf("~type should harvest nothing (inert), got %#v", res.Entries)
		}
	})

	t.Run("macrohint", func(t *testing.T) {
		res := ev.Evaluate(`(~macrohint toplevel)`, parseForm(t, `(~macrohint toplevel)`))
		if len(res.Diags) != 0 {
			t.Fatalf("unexpected diags: %v", diagMsgs(res))
		}
		if got := entryString(t, res, "macrohint"); got != "toplevel" {
			t.Fatalf("macrohint = %q, want toplevel", got)
		}
	})

	t.Run("doc", func(t *testing.T) {
		res := ev.Evaluate(`(~doc 'hello there')`, parseForm(t, `(~doc 'hello there')`))
		if len(res.Diags) != 0 {
			t.Fatalf("unexpected diags: %v", diagMsgs(res))
		}
		if got := entryString(t, res, "doc"); got != "hello there" {
			t.Fatalf("doc = %q, want 'hello there'", got)
		}
	})

	t.Run("pure", func(t *testing.T) {
		res := ev.Evaluate(`(~pure)`, parseForm(t, `(~pure)`))
		if len(res.Diags) != 0 {
			t.Fatalf("unexpected diags: %v", diagMsgs(res))
		}
		v := entryValue(t, res, "pure")
		if b, ok := v.Val.(bool); !ok || !b {
			t.Fatalf("pure = %#v, want bool True", v)
		}
	})

	t.Run("desc", func(t *testing.T) {
		res := ev.Evaluate(`(~desc count 'how many')`, parseForm(t, `(~desc count 'how many')`))
		if len(res.Diags) != 0 {
			t.Fatalf("unexpected diags: %v", diagMsgs(res))
		}
		if got := dictString(t, entryValue(t, res, "desc"), "name"); got != "count" {
			t.Fatalf("desc.name = %q, want count", got)
		}
		if got := dictString(t, entryValue(t, res, "desc"), "text"); got != "how many" {
			t.Fatalf("desc.text = %q, want 'how many'", got)
		}
	})

	t.Run("flag with default", func(t *testing.T) {
		res := ev.Evaluate(`(~flag mode (default :strict) :loose :off)`,
			parseForm(t, `(~flag mode (default :strict) :loose :off)`))
		if len(res.Diags) != 0 {
			t.Fatalf("unexpected diags: %v", diagMsgs(res))
		}
		fl := entryValue(t, res, "flag")
		if got := dictString(t, fl, "arg"); got != "mode" {
			t.Fatalf("flag.arg = %q, want mode", got)
		}
		if got := dictString(t, fl, "default"); got != ":strict" {
			t.Fatalf("flag.default = %q, want :strict", got)
		}
		if got := dictStrings(t, fl, "values"); len(got) != 3 ||
			got[0] != ":strict" || got[1] != ":loose" || got[2] != ":off" {
			t.Fatalf("flag.values = %v, want [:strict :loose :off]", got)
		}
	})

	t.Run("flag without default", func(t *testing.T) {
		res := ev.Evaluate(`(~flag mode :loose :off)`, parseForm(t, `(~flag mode :loose :off)`))
		if len(res.Diags) != 0 {
			t.Fatalf("unexpected diags: %v", diagMsgs(res))
		}
		fl := entryValue(t, res, "flag")
		if v := dictGet(t, fl, "default"); v.Kind != core.KindNil {
			t.Fatalf("flag.default = %#v, want Nil", v)
		}
		if got := dictStrings(t, fl, "values"); len(got) != 2 {
			t.Fatalf("flag.values = %v, want 2 values", got)
		}
	})

	t.Run("sig is inert (disconnected, Phase 4)", func(t *testing.T) {
		// ~sig is disconnected (TypeSignatures.md Phase 4): the macro is kept in
		// annot.phl but harvests nothing — inline fun/method signatures carry the
		// type now. Both the function form and the no-params form harvest nothing.
		for _, body := range []string{`(~sig (num num) num)`, `(~sig () str)`} {
			res := ev.Evaluate(body, parseForm(t, body))
			if len(res.Diags) != 0 {
				t.Fatalf("%s: unexpected diags: %v", body, diagMsgs(res))
			}
			if len(res.Entries) != 0 {
				t.Fatalf("%s: ~sig should harvest nothing (inert), got %#v", body, res.Entries)
			}
		}
	})
}
