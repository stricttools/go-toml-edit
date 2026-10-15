# Publicity and later 0.x work: remaining scope from the founding document

> Split from todo/original-idea-remaining.md; the error-handling section is
> done and moved to todo/.done/error-handling-founding-scope.md. The sections
> below stay active and unscheduled. Text preserved verbatim from the founding
> document.

## Distribution and Publicity

### Publicity

- Submit to awesome-go (https://github.com/avelino/awesome-go) -- the most visible curated Go library list
- Post announcement to r/golang with a clear problem statement and comparison to existing libraries
- Submit to Go Weekly newsletter (https://golangweekly.com/) -- reaches ~30k Go developers
- Write a blog post framing the problem: "Why can't Go edit TOML files without destroying comments?" -- establish the gap, reference tomlkit as proof it's solvable, show go-toml-edit as the Go answer, include before/after examples
- Post to Hacker News (Show HN) -- TOML tooling and comment preservation are topics that resonate with the HN audience
- Open an issue or discussion on pelletier/go-toml linking go-toml-edit as the editing solution they explicitly declined to build
- Submit to TOML's wiki/ecosystem page (https://github.com/toml-lang/toml/wiki) if a Go section exists
- Tag relevant Go influencers / TOML maintainers on the announcement

## Later 0.x work

Struct-to-TOML Marshal, formerly listed here, is tracked in
todo/deferred-redesign-work.md.

- TOML 1.1 support when the spec stabilizes
- Public streaming/event parser for power users (linters, formatters, custom tooling)
- Performance optimizations (lazy parsing for large files)
