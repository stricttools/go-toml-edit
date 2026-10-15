# Deferred work from the strictness-and-fidelity redesign

## Context

Items deliberately NOT pursued during the redesign campaign recorded in
todo/.done/design-record.md. Each was considered and deferred (or rejected,
where marked) with the reasoning below. The campaign shipped in 0.4.0; each
item can be picked up independently.

## Deferred items

- **Struct-to-TOML Marshal.** Struct tags, nested sections,
  caller-controlled key ordering, comment emission. The map-only Marshal
  was deleted (zero callers; forced alphabetization contradicted the one
  plausible use). A consumer's large hand-built document exporter
  could shrink substantially onto a struct Marshal. Effort: the largest
  greenfield design here — ordering and comment-emission APIs are open
  design space.
- **Remaining structural manipulation.** Node moves across containers,
  positional insertion, table-to-inline (and reverse) conversion. Deferred
  for zero demonstrated demand; conversion semantics (what happens to
  comments) are open design questions. Effort: medium per operation.
- **Declarative key-ordering convenience.** A name-list ordering operation
  (order these keys; a mandatory policy for unlisted ones). Deferred by
  ruling, and it must be designed only AFTER the structural comment model
  below settles — never before, or its comment sub-rule re-imports the
  ambiguity the permutation-only ruling avoided. Effort: small once the
  comment model exists.
- **The structural comment model.** Containers hold only key-value
  children plus a trailing-trivia slot; per-comment attachment direction
  becomes parser-set, correctable data (blank-line adjacency as evidence).
  Makes comment-separation bugs unrepresentable instead of tested-against.
  This is the ruled direction if comment-placement demand materializes;
  its one blind spot (trailing explanation comments) is what the
  attachment-direction data solves. Effort: large — a node-model change
  with fanout into parser, render, and every structural test.
- **External node construction with spelling control.** The pre-built-Node
  input branch of Set/SetCreate was deleted in the wave with no
  replacement, so there is no spelling for writing a
  literal-quoted string or a hex integer programmatically. If demand
  appears, the shape is constructor functions with style/base parameters
  feeding the canonical-rendering path. Effort: small-to-medium.
- **Splice API.** Replace an exact byte span in place (with re-validation).
  One consumer implements this format-neutrally for itself, so the
  extraction payoff is partial by design. Effort: small, but the
  validation semantics need care.
- **Finer decode independence semantics.** Strict decode collects
  independent violations and stops descending below an errored node; if a
  consumer wants finer collection semantics (e.g. partial descent below
  certain error kinds), that refinement is additive on the aggregate error
  type. Effort: small.
- **Incremental read-layer invalidation.** The wave accepts whole-layer
  rebuild on any mutation (quadratic for alternating edit/read loops).
  Incremental invalidation removes that cost. Effort: medium-to-large;
  design against the fold's structure.
- **Pre-parsed path type.** A parsed-path value accepted by Lookup/Has and
  the getters, making hot-path reads allocation-free (the budget is
  "no allocations beyond path parsing"). Effort: small.
- **Enum-in-descriptor — OPEN DECISION.** Should the descriptor admit a
  closed value set (choices) per field? Pros: kills the most common
  residual app-side check (several consumers validate choice fields
  against literal or runtime-supplied lists). Cons: the first step past
  the ruled engine scope (presence/type/required only); the next request
  (uniqueness, then ranges) cites this one as precedent; choice-violation
  message wording becomes the engine's problem. Not rejected — undecided.

## Rejected (recorded so they are not re-proposed)

- **Presence-reporting API** (which paths did decode populate). Rejected:
  required-key support plus EnsureDefaults absorbed the need; the residual
  consumer case is domain logic on a data shape where pointer fields are
  the better instrument (repeated array-of-tables elements make path-keyed
  reports clumsier than per-struct pointers).
- **Kernel-level crash-injection harness for WriteFile.** Rejected: timing
  dependent and OS-specific — flaky in CI; the deterministic invariant
  set covers the contract.
- **Struct-size budget test.** Rejected: a hand-maintained number that
  breaks on every legitimate change and differs across architectures.
