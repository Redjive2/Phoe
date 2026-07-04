1. Multiple-implementation syntax for functions and methods:   ---DONE--- `(let name (params) [where guard] = body)` clauses, tried in declaration order; first pattern+guard match wins; linter requires a catch-all when coverage is undecidable (non-exhaustive-clauses).
```pho
(fun add (Number Number) Number)
(let add (a b) where (== b 0) = a)
(let add (a b) where (== a 0) = b)
(let add (a b) = (+ a b))
```

2. Matching syntax for functions and methods:   ---DONE--- literal patterns (numbers/strings/atoms/bools/none/type values), `(Type name)` type tests, `[p …]` list destructures, and `Type.{ field = pat }` struct destructures in clause param lists. (Tuple `:[…]` patterns await §6 tuple lexing.) These same patterns also destructure in **`let` assignment targets** — `(let [a b c] = xs)`, `(let (Number n) = 5)`, `(let Type.{ (field) = pat } = v)` — sharing three helpers everywhere: `(var name)` binds reassignably (bare binders are const in a `let`), `(Type name)` annotates (erased in a `let`, a runtime test in a clause/`select`), and the `()` capture operator on a struct-field key binds the whole field value alongside its pattern. (The retired ungrouped `(let Type name = value)` is gone — the grouped `(Type name)` is the only typed-binding form.)
```pho
(fun add (Number Number) Number)
(let add (a 0) = a)
(let add (0 b) = b)
(let add (a b) = (+ a b))

(struct Deep-Box {
    inner :['key' -> Integer]
})

(method Deep-Box.get-data (Self) Integer)
(let Deep-Box.get-data (Self.{ inner = :['key' -> value] }) = value) -- doesn't match `inner`, since `inner` is a struct field name with an = after it (just writing Self.{ inner } *would* match inner, though.)
```

3. Matching syntax in bodies:   ---DONE--- `(select v case PAT -> RESULT …)`; first match wins; `do` results stop at the next `case`; unmatched select is a runtime error.
```pho
(fun add (Number Number) Number)
(let add (a b) =
    (select [a b]
        case [0   rhs] -> rhs          -- context-aware do notation here: stops at next `case` if one is found
        case [lhs 0]   -> lhs          -- breaks after first match, returning the result from the select expression
        case [lhs rhs] -> (+ lhs rhs)
    )
)
```

4. New / syntax for static fields:
```pho
(struct Empty {})

(method Empty.say-hi () 'hi')
(static method Empty/say-bye () 'bye')
```

New .as operator for up- and down- casting:
```pho
-- create a concrete type
(template T)
(struct Box { T #inner })

-- create a trait
(template T)
(trait To
    (method Self.to (Self (disc T)) T)
)

-- set up the trait implementation
(template T)
(method (Box T).to (Self (disc T)) T)
(let (Box T).to (self) = self.#inner)

-- create a value of a concrete type, then cast it to a trait object, then cast it back
(let box           = Box.{ #inner = 1 })          -- type is (Box Number), implements (To Number)
(let trait-box     = (box.as (To Number)))        -- type is (To Number), can still be cast back to (Box Number)
(let retrieved-box = (trait-box.as (Box Number))) -- can fail (failure modes to be handled later, panic for now)

-- also works on types:
-- create a trait with type-level (static) members to make this feature useful
(template T)
(trait From
    (static method Self/from (T) Self)
)

-- set up the trait implementation
(template T)
(static method (Box T)/from (T) Self)
(let (Box T)/from (value) = Box.{ #inner = value })

-- cast (Box Number) to (From Number), not necessary here but useful for disambiguation
((Box Number).as (From Number)) -- this expression is typed as a trait object type (From Number) backed by a concrete type (Box Number); returns (Box Number) when .from is called.
```

5. New generic filling syntax (remove, don't deprecate, the old syntax):
```pho
(template I O)
(struct Operation {
    I           #input
    (Fun (I) O) #op
})

(template I (From<I> O))
(static method Operation<I O>/convert (I) Self)

(let Operation<I O>/convert (input) = do
    (let op = O/from<I>)  -- resolving trait member: the <I> fills the trait bound and means (O.as From<I>).from
    Self.{
        #input = input
        #op    = op
    }
)
```

6. New types:
```pho
(fun returns-int () Integer) -- signed   int64
(fun returns-byte () Byte)   -- unsigned int8
(fun returns-float () Float) -- float64
(fun returns-num () Number)  -- now a trait type for +, -, etc

(template T)
(fun returns-collection () Collection<T>)

(template T)
(fun returns-list () [T])

(template I O)
(fun returns-dict () [I -> O])

(fun returns-fun () (fun (Integer Number) None))  -- instead of (Fun (I) O)

(fun returns-effectful-fun () (fun! () None))

(fun returns-method () (method Unknown (Self) None))
(fun returns-effectful-assigning-method () (method!= Unknown ((var self)) None))

(fun returns-char () Character)

(fun returns-tuple () :[Integer Character Unknown Dynamic])  -- can only be keyed with parse-time expressions, like :['a'].[0], NOT :['a'].[some-index-var]
(fun returns-tuple-dict () :[:a -> Integer :b -> Character]) -- Same rule from before goes for creating keys: :[some-var -> some-val] is bad; :['some-key' -> some-val] is fine
```

7. Operator overloading:   ---DONE (concrete types) --- `(operator Recv.OP (Self …) Ret)` declares an overload the adjacent `(let Recv.OP (self …) = body)` clauses implement (it lives in the type's method table under OP). The primitive prefix operators (`+ - * / mod < <= > >= == ~=`) dispatch to it when the first operand is an instance of that type; the index forms `recv.[i]` (read → `[]`) and `(= recv.[i] v)` (write → `[]=`, a `(var Self)` receiver) dispatch too. Runtime + linter both wired; `[]=` counts as a self-mutation suffix. STILL TODO: the generic `My-Collection<L R>.[]` forms below — they need §5 (generic filling), not yet built.
```pho
(struct My-Number { Number #n })

(operator My-Number.+ (Self Number) Self)
(let My-Number.+ (self other) = 
    My-Number.{ #n = (+ self.#n other) }
)

(template L R)
(struct My-Pair {
    L #left
    R #right
})

(template L R)
(operator My-Collection<L R>.[] (Self Integer) (Or L R)) -- read @ index

(template L R)
(operator My-Collection<L R>.[]= ((var Self) Integer (Or L R)) (Or L R)) -- write @ index
```

8. New universal Error trait and ? meaning:
```pho
(trait Error
    -- whatever should be here
)

(type Failed-To-Add = (or :idk :how :one :fails :to :add :numbers))

-- ? suffix should now mean 'can return a type implementing Error' not 'returns boolean'. Should be enforced.
(fun fallible-add? (Number Number) (or Number Failed-To-Add))
```

9. Function overloading (NOT just implementation overloading):   ---DONE--- multiple `(fun name (Types…) R)` sigs per name; call dispatches on runtime arg types; MOST-SPECIFIC sig wins (pairwise subtype); ambiguity is an error. Also: defaults moved into sigs — `(optional Type else DEFAULT)` (none-coalescing; replaces the impl-side `(or p d)`), and `(const T)` sig slots replace the retired `(disc X)` (call sites pass parse-time constants; clauses dispatch on them via literal patterns, coverage may be partial).
```pho
(fun add (Number Number) Number)
(let add (a b) = (+ a b))        -- *all* these must *directly* follow their respective declarations now, of course, regardless of whether or not this feature is used.

(fun add (String String) String)
(let add (a b) = (+ a b))
```

10. Strictly enforced kebabing for types:   ---LINTER DONE--- a type name that is not Title-Kebab-Case draws `bad-type-name` (error) from the linter/LSP, at BOTH declarations and references. Declarations: `struct`/`type`/`trait`/`template` names. References: signature type slots + result (erased-sig pass `checkSigTypeNames`/`checkTypeExpr`), method-receiver types, and every walked type reference (`.is?`/`.as`, `subtype?`, construction, type-alias RHS, pattern type tests — a leaf resolving to a `DefStruct`/`DefType`). Classification is `syntax.ClassifyIdent` (kebab value vs Title-Kebab type vs invalid). The reader-level hard rejection (`syntax.StrictNames`, "won't parse") stays off until §12 goimport re-casing, since the `dep.*` Go-method names are still PascalCase.
```pho
My-Type -- fine
MyType  -- won't parse, LSP/linter gets angry
```

11. Lambda builtin   ---DONE--- a first-class anonymous callable with a FLAT header (no param-list parens): `(lambda[?!=] [RecvType] [self] params… [RetType] -> body)`. The literal `self` (optionally preceded by a receiver type; `Self` = inferred) marks a receiver; each param is a `(Type name)` typed param or a bare name; a trailing Capitalized leaf is the return type; the `!`/`=`/`?` suffix declares effects. Types/receiver/return are optional and inferred from context. Replaces the anonymous `(fun …)`/`(method …)` value forms.
```pho
([1 2 3].map (lambda (Integer n) Integer -> (+ n 1)))   -- typed param + return type (both inferable, so optional)
([1 2 3].map (lambda n -> (+ n 1)))                     -- inferred arg + return types; used when & syntax wont work
(lambda Integer self other -> (some-op self other))     -- explicit receiver type Integer, receiver `self`
(lambda self other -> (some-op self other))             -- inferred receiver type
(lambda! -> (core/print-line! 'hi'))                    -- no params, effectful
(lambda= self new-value -> (= self.inner new-value))    -- receiver + self-mutation (can combine ?!= in that order)
```

12. go-import re-casing and effect management:
```pho
(goimport 'gopkg')

(fun do-stuff! () None)
(let do-stuff! () = do
    (gopkg/cause-effect!)  -- get it? anyway, this symbol is automatically recased from CauseEffect and the ! is added because the real symbol is CauseEffect_E (for Cause Effect [E]ffectful). Same works for = (_A for [A]ssigns) and ? (_F for [F]allible), and these are automatically enforced by function signature, specifically: ! -> takes module as ptr, = -> takes first arg as ptr (must start with TypeName_M_ for method syntax), and ? -> returns any fallible type (good luck). Can be combined in the same order _FEA that Pho uses. 
)
```
