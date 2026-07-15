# Repository Guidelines

## Project Structure & Module Organization

This is a Go 1.26 module. The module root (`engine.go`, `types.go`, and related files) is the stable public `music2bb` package. `cmd/music2bb` contains process startup for the CLI, while `internal/cli` owns command parsing and terminal interaction. Core orchestration lives in `internal/service`; concrete Kugou, Bilibili, browser, matcher, configuration, and HTTP implementations live in their corresponding `internal/*` packages and are assembled by `internal/wiring`. Keep site-specific protocols out of the public package. Architecture details are in `docs/architecture.md`. Fixtures belong in package-local `testdata/` directories.

## Build, Test, and Development Commands

- `go build -trimpath -o music2bb ./cmd/music2bb` builds the local CLI.
- `go run ./cmd/music2bb version` runs a command without creating a binary.
- `go test ./...` runs deterministic unit, fixture, and boundary tests.
- `go test -race ./...` checks concurrency-sensitive code.
- `go vet ./...` performs the static checks required by CI.
- `go test -run '^$' -tags='live authenticated browser_install' ./...` verifies that optional tagged tests compile.

Run `gofmt -w <files>` on changed Go files before submitting. Use `go mod tidy` only when dependencies change.

## Coding Style & Naming Conventions

Follow standard Go formatting and idioms: tabs from `gofmt`, short lowercase package names, `PascalCase` exported identifiers, and `camelCase` internal identifiers. Add concise doc comments to exported APIs. Define interfaces where they are consumed, especially in `internal/service`, and inject external I/O so default tests remain offline. Preserve caller ownership at the public boundary by returning copied public types rather than internal models.

## Testing Guidelines

Place tests beside code as `*_test.go`; name cases `TestBehaviorCondition`. Prefer table-driven tests for parsing and scoring variants, `t.TempDir()` for filesystem state, and local HTTP test servers for remote clients. Default tests must not require credentials or network access. Read-only canaries use the `live` tag. Tests tagged `authenticated` may create temporary Bilibili resources and require `MUSIC2BB_RUN_AUTH_CANARY=1`; never enable them casually.

## Commit & Pull Request Guidelines

History follows Conventional Commit-style subjects, commonly `fix(cli): ...`, `test(bilibili): ...`, `refactor: ...`, and `docs(license): ...`. Keep commits focused and use an applicable package scope. Pull requests should explain behavior changes, identify affected packages, list validation commands, and link relevant issues. Include terminal output for CLI presentation changes; screenshots are only needed when visual behavior is involved. Do not commit cookies, local keyword overrides, browser archives, binaries, or files under `dist/`.
