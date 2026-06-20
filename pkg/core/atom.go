package core

import "sync"

// Atom is an interned, immutable symbolic value written `:name` or `:123`
// in source. Atoms exist to be cheap to pass around and trivial to compare:
// two atoms with the same name share one *Atom (see Intern), so equality and
// dict-key hashing reduce to Go's native pointer comparison — O(1) regardless
// of name length. The name is never mutated once interned.
//
// Atoms are restricted to identifier or all-digit forms (validated by the
// leaf evaluator and by str->atom); the runtime stores the name verbatim, so
// `:01` and `:1` are distinct atoms.
type Atom struct {
	name string
}

// Name returns the atom's text (without the leading ':').
func (a *Atom) Name() string { return a.name }

// The intern table is a process-global symbol table. Atoms are never
// removed: like every language's symbol table this is a deliberate trade —
// the set of source-literal atoms is bounded, and str->atom validates its
// input so the pool can't be flooded with arbitrary strings. The mutex
// guards against concurrent host goroutines (goop streams); eval itself is
// effectively single-threaded, so the lock is uncontended in practice.
var (
	atomMu   sync.Mutex
	atomPool = map[string]*Atom{}
)

// Intern returns the canonical *Atom for name, creating it on first use.
// Callers must pass an already-validated atom name (identifier or digits).
func Intern(name string) *Atom {
	atomMu.Lock()
	defer atomMu.Unlock()
	if a, ok := atomPool[name]; ok {
		return a
	}
	a := &Atom{name: name}
	atomPool[name] = a
	return a
}
