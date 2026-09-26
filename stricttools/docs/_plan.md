# Implementation plan: the strictness-and-fidelity redesign

This file is the execution sequence with per-phase verification. The design
record that served as the binding specification during the build is retired
to `todo/.done/design-record.md` (ruled 2026-09-05: it was scaffolding for
the build; with the build done, the truth lives in the code, the tests, the
API snapshot and the docs). The plan contains no open decisions and no
todo-filing steps.

Execution conventions: implementor+auditor subagent pairs per phase;
authoritative test runs only on a settled tree; one commit per work item
via safegit, each followed by its `rlsbl changelog add` entry (breaking
user-facing changes typed `breaking`); no pushes ever — the release is the
only push, at the end. Mechanical sweeps (renames, signature conversions)
run scripted with a dry run, asserted per-file occurrence counts, and a
reviewed diff.

## Phase 0 — Foundations (COMPLETE)

Ledger backfill (archives anchored, descriptions authored, changelog
regenerated; `rlsbl status` green). Design record authored, reviewed, and
final. Test-infrastructure hardening: gofmt sweep; staticcheck + gofmt CI
steps; two-version CI matrix (floor 1.23 + stable) with the go directive
at 1.23; `SkipTable` renamed `ErrSkipTable`; the compliance skip set
derived from toml-test's own version-filtered listing with case counts
asserted; one shared corpus-walk helper; one shared fuzz seed corpus;
drift-prone counts removed from the docs template.

## Phase 1 — API-snapshot baseline

Create the exported-API snapshot test: a committed listing of the exported
surface, generated from the package, with a freshness assertion. Capture
the BASELINE snapshot from the last released version (v0.3.0) as a
committed artifact before any rename — the release's migration table is
the diff of final-vs-baseline snapshots, extended with the decode-time
breaks the consumer check discovers.
Verify: the snapshot test fails on any exported-surface change until the
snapshot is regenerated; the v0.3.0 baseline artifact is committed.

## Phase 2 — Mechanical rename pass

`DocumentNode` → `Document` and `Rename` → `RenameKey`, package-wide
including tests and hand-written docs sources, as a scripted sweep (dry
run, occurrence counts, reviewed diff).
Verify: suite green; old identifiers absent from the package and docs
sources; snapshot regenerated.

## Phase 3 — Diagnostic contract

The unified `Error` type per the record's API block (kind set, Position
with Offset, snippet, expected/got, value, keys, wrapping; pointer
receivers; `Err*` sentinels with the kind/sentinel drift test), replacing
`ParseError`. The `Errors` aggregate with its Unwrap/As/Is contract. Every
parser-stage error site routed through the one constructor (source-walk
test), all sites carrying offset and snippet. Token offset and span-offset
population everywhere. `ParseFile` with filename flowing into every later
diagnostic from that document. Parse stays first-error.
Verify: error-contract tests (representative error from each producing
surface through errors.Is and errors.As); span tests assert offsets;
constructor source-walk test green; existing error-asserting tests updated
and green.

## Phase 4 — The read-layer and path re-basing

The fold as a separate post-parse pass implementing the record's rule list
(first-appearance order, dotted expansion, implicit records with
first-header-wins, last-entry addressing, inline folding, per-key spans,
hard internal errors on impossibilities), with `Record`/`Entry` per the
API block (including `Kind()`, `RecordsSpan()`, comma-ok `Node()`).
Uncached at this stage (every access rebuilds; caching arrives in Phase
7). Path resolution re-based on the layer: `Get` deleted; `Resolve` errors
on logical-only paths with the wrong-container kind; `Lookup`/`Has`
answer concrete-node existence; edit paths keep collection addressing;
virtual view types retired from resolution (deleted fully in Phase 11).
The Cursor re-based internally on layer positions. Path helpers exported
(`ParsePath`, `JoinPath`, `PathSegment`); the diagnostic path renderer
uses them.
Verify: the fold suite (including the per-case reference-tree comparison
over the valid toml-test corpus and the worked examples from the record)
green; existing document/path/
cursor tests green with only the enumerated behavioral edits (widening
flips land in Phase 10; here only resolution semantics); path round-trip
and diagnostic paste-back tests green.

## Phase 5 — The decode engine and descriptor

The descriptor core (`Spec`/`Field`/`FieldAny`, four date-time kinds, the
licensed-nil `Dynamic`, construction-time errors for missing sub-
descriptors) validating over the read-layer; collect-independent error
reporting per the record (continue across siblings, never descend below an
errored node, document order, aggregate with first-rendered). The struct
front end deriving descriptors via reflection, preserving embedded
promotion, hooks, and pointer targets, with EXACT-ONLY key matching (the
existing decoder's case-insensitive fallback deleted per the record);
exclusions and unknown tag options as hard errors, the tag on an unexported
field inert; the `Unmarshaler` hook applying at the root target too, and a
hook's error collected as `KindHookError`; the conversion table implemented
once and consumed by both stages. `Validate`, `Decode`, `Unmarshal`,
`DecodeNode` per the API block. The old structure-re-deriving decode
families deleted. Red-green within this phase: the four decode bugs
(map-of-struct truncation, array-of-tables under a plain table, the
map-element panic, fixed-array under-fill) — public-surface tests
committed red first with output in the commit message, green under the
engine; the weak Unmarshal==Decode audit test strengthened to assert
values; the leniency tests flipped.
Verify: engine suite covering every violation kind with positions; the
cross-front-end identical-diagnostics test; the hand-built-descriptor
test; the conversion-table cross-family test; full suite + corpus green;
benchmarks compile and run.

## Phase 6 — Structural operations and the write surface

`PermuteChildren` (gather semantics, total bijection, concrete containers,
the drift note), `AppendToArray`, `RemoveFromArray`, the `[]Pair` ordered
inline-table input (duplicate-key refusal), `EnsureDefaults` with
`[]Default` (ordered, full paths, standard-table intermediates,
partial-application contract). The pgdesign reorder acceptance test
(grouping with comments traveling, drops composed as delete-then-permute)
BEFORE freezing the op signatures — the record marks them provisional
until it passes. The small write-path fixes: unsigned overflow refusal,
`NewTable` array-of-tables collision refusal, the inline-table
delete-missing-key dirty fix (each red-green).

Fold-aware edit refusals (added after the Phase 4 audit found eleven
public edit sequences that build an unfoldable document, leaving `Root()`
to panic and the document unrepairable): every edit operation that can
create a binding conflict refuses instead — `NewTable`/`NewArrayTable`
refuse exactly what the parser's definition tracker refuses (a name whose
FINAL key redefines a value, record, collection, or dotted-implied table;
descending through a dotted-implied prefix to a deeper sub-table is legal
per the compliance corpus), `RenameKey` to a name bound by any construct
kind (not just key-value siblings), and dotted-key/table duplicate
collisions — each refusal `KindConflict` per the record's definition,
each red-green against its audited sequence. `Set`/`SetCreate` refuse
with `KindWrongContainer` when the TARGET NAME itself is logical-only (an
array-of-tables collection name, a name a longer header implies, a
dotted-key prefix); writes whose PARENT record merely lacks a concrete
node succeed per the record's write-path policy (existing keys reachable;
new keys via dotted extension or anchoring-header materialization).
`Delete`'s fold-failure swallow is removed so a fold error surfaces
instead of reading as "parent not found". The Set-on-header-table case
is RULED (user, 2026-09-04): `Set`/`SetCreate` targeting a name bound by
a concrete header table REFUSES with `KindWrongContainer` — value writes
touch value fragments; structural constructs change only via structural
operations or an explicit `Delete`. Red-green like the rest of the
family.

Also here, one ruled kind change riding this phase: a comment write inside
an inline table refuses with `KindWrongContainer` instead of the
`KindConflict` the code returns today — one line plus its test update, per
the record's write-path section (an inline table structurally cannot host a
comment; same family as the array-index rename refusal).

Verify: op tests incl. bijection violations by kind; the acceptance test
green; determinism assertion for `EnsureDefaults`; none of the eleven
audited panic sequences can panic `Root()` — each refuses with its ruled
kind.

## Phase 7 — Node model

Interface split (`Node` loses `Value()`; `Scalar` embeds `Node`; per-type
disposition per the record; Diff's internal read moves to `Scalar`), field
unexporting with the accessor block (returned slices are copies, tested),
the scalar payload+lexeme struct and the structure mutator as the only
write paths (source-walk tests), dirtiness propagation via parent
references (constant-time check, counter-asserted), fragment state per the
vocabulary (including blank-line runs, held as bytes, in trivia), the
comment-API respelling (path-based public setters; node-level unexported;
`Comment()`/`LeadingComments()` re-purposed as the normalized getters),
deletion of the pre-built-Node input branch, and the
read-layer caching switch (synchronized, generation-keyed; `Parse` alone
builds nothing, test-asserted; the concurrent-reads race test unchanged
and green). Once parent references exist, `DecodeNode` diagnostics gain
the document's filename (the Phase 5 audit found they carry none — a
`Node` has no back-reference to its document until this phase).
Verify: reflection test (no exported fields on Node implementers); both
source-walk tests; propagation counter test; race test green under
`-race`; full suite green; a `DecodeNode` diagnostic from a
`ParseFile`-loaded document carries the filename.

## Phase 8 — Rendering, Set-equality, canonicalization

Fragment-based rendering (clean fragments splice; header key raw parts
wired into both header types; inline-table keys from raws; `RenameKey`
invalidates only the renamed part). Canonical rendering per kind exactly
as the record states (string/integer/float algorithm/boolean/date-times;
specials `nan`/`inf`/`-inf`; total `FormatFloat`). The exported renderers
(`QuoteString`, `QuoteKey`, `FormatFloat`). The Set-equality contract
(byte-equality no-op for `Set`/`SetCreate`, canonical-rendering fallback,
wholesale container replacement, sign-bit NaN refusal, no-op never clears
dirtiness, `Delete`-missing stays a silent no-op) in code and doc
comments.
Verify: the corpus battery — untouched round-trip (`Parse(x).Bytes()==x`),
value-mutation sibling isolation, trivia-mutation value isolation, rename
construct isolation, renderer correctness with lexeme splicing disabled
(semantic equality + idempotent re-render), stale-lexeme new-value pass,
Set-then-Set byte stability, canonical same-value no-op — all green over
the full valid corpus; renderer unit tests per canonical rule.

## Phase 9 — Formatter

Blank-line grouping preservation (runs collapse to one; insertion-only
table-blank-line option; the spurious-blank-line fix pinned via an edit
sequence).
Verify: the option-combination test over a blank-line fixture; format
suite updated and green.

## Phase 10 — Accessors

The staged navigate/type-check/convert internals shared with the engine;
`(T, error)` on path-level, node-level (`As*` family incl. the Local
types), and Cursor terminals (short spellings; `Err()` for
navigation-terminated chains); the widening flips; `Lookup`/`Has`
allocation assertion (warm cache); `WriteFile` with its full invariant
set.

The decode entry-point reshape, per the record's decode-error-reporting
section, rides this phase because it is the same `(T, bool)`-to-`(T, error)`
family of signature work: `Unmarshal`, `Decode`, and `DecodeNode` become
VALUE-RETURNING generic forms (`Unmarshal[T](data []byte) (*T, error)` and
its two siblings — exact spellings settled at implementation against the
record, which holds the shape, not the letter), so a failed decode leaves no
caller-owned target to observe. Because Go has no parameterized methods, the
document- and node-level forms become PACKAGE FUNCTIONS taking the document
or node as their first argument. A seed-factory form (`DecodeOver`: the
caller supplies a factory building the value the decode overlays) covers the
defaults-overlay pattern the value-returning form otherwise loses.
`DecodeSpec` is added on the descriptor path — atomic, no partial map on
error. Written-path reporting has no failure side to report from once a
failed decode is unobservable, so it is `DecodeOver`'s second result: the
paths the decode wrote, in document order, nil on failure. The doc comments'
partial-write sentence ("do not use the target
after an error") is REPLACED by the unobservability contract, and the
consumer break list of Phase 13 grows by every decode call site.
Verify: per-stage kind tests; the scripted `(T, bool)`-to-`(T, error)`
call-site sweep (dry run, counts, reviewed diff); WriteFile invariant
tests incl. injected failures; the reshaped entry points covered by the
existing decode suites (values asserted, not just error-ness), a
seed-factory test, a `DecodeSpec` atomicity test (an errored decode returns
no map), and a written-path test over a fixture where the document overrides
part of a seed; snapshot regenerated; full suite green.

## Phase 11 — Ports, deletions, final sweep

Merge re-based on the layer; Walk documented as the syntactic traversal
(kept on the AST); Diff pinned (int≠float; spelling-pairs equal).
Deletions: path-based `Items`/`Len`, `Marshal`, the retired virtual views,
the token vocabulary and `Trivia` unexported — as one scripted identifier
sweep with the usual discipline.
Verify: ported/pinning tests green; snapshot regenerated and shows exactly
the ruled surface; staticcheck clean (catches stranded dead code).

## Phase 12 — Documentation truth

Sweep the hand-written docs sources (`.stricttools/docs/_README.md`, `.stricttools/docs/_CLAUDE.md`,
`.stricttools/docs/usage-guide.md`, `.stricttools/docs/design.md`, the package doc comment,
examples, `benchmarks.txt`) against the shipped behavior: strictness, the
error contract, the read-layer, the two-surfaces contract sentence, the
Set contract, deletions and renames, the named-import README example and
the no-runtime-dependencies statement; regenerate root files via bare
`selfdoc gen`; refresh or de-number the benchmark table; runnable
`Example` functions for each new top-level entry point.
Verify: `git grep` over the changed names comes back clean outside
generated files; examples compile and run; every new exported identifier
has a doc comment (vet clean).

## Phase 13 — Pre-release consumer check

For every consumer (a repo with a Go file importing this package): a
scratch-clone build against the pre-release tree via a temporary replace
directive (never committed), capturing compile breaks; then that
consumer's own test suite and fixture corpus against the same tree,
capturing decode-time breaks. The combined output drives the sweep briefs
and extends the migration table.
Verify: a per-consumer break list exists; no unexplained break (every item
maps to a ruled change).

## Phase 14 — Release

Todo state first: the strict-by-default-unmarshal todo moves to done; the
conformance/round-trip todo splits (round-trip half done; conformance half
stays active awaiting the spec); the founding-remainder todo splits
(error-handling section done). Changelog coverage complete (every commit
covered; breaking entries typed). Release file with description and
context, minor bump, then
`rlsbl release run --no-allow-dirty --watch --approve-consequential`.
Verify: release completes green through CI; the new version resolves from
the proxy.

## Phase 15 — Consumer sweep (separate go; after the release)

Order per the record: strictcli + its harness in one commit, then RELEASE
strictcli; safegit in the same working session (its workspace uses the
strictcli checkout); then each remaining consumer bumps both module
requirements in one commit — wavescript (delete the strict-decode layer
and the typed-read/enumeration/comment/render/write-cycle helpers; migrate
the two `Len` sites), pgdesign (minimal-correct: drop the excluded-field
workaround and two-pass decode, strip `omitempty` options, mechanical
conversions; validator untouched), howmuchleft (hard error on invalid
config; `EnsureDefaults`; plain fields; the profile fix named in its
changelog; preserving write-back), dirstat (descriptor; app-side value
rules), strictspec (thin conversion from the read-layer; offset workaround
deleted; collection-span end reconciled). saferm gets `go work sync` plus
the strictcli bump. Each consumer's suite green before its commit.
