# Architecture

`kg2bb` separates terminal interaction, application orchestration, and external
site protocols so the conversion engine can be reused by another Go frontend.

## Dependency direction

```text
cmd/kg2bb
├── internal/cli
│   └── kg2bb (module root)
└── kg2bb (module root)
    ├── internal/service
    ├── internal/wiring
    ├── internal/model
    ├── internal/config
    ├── internal/kugou
    ├── internal/bilibili
    └── internal/browser

internal/wiring
├── internal/service
├── internal/kugou
├── internal/bilibili
├── internal/browser
├── internal/matcher
├── internal/config
└── internal/netx
```

The CLI depends on the public root package and does not call site clients
directly. The service layer depends on interfaces rather than concrete clients;
production implementations meet those interfaces through `internal/wiring`.
Packages under `internal` are implementation details and cannot be imported by
consumers outside this module.

## Package responsibilities

| Package | Responsibility |
|---|---|
| `kg2bb` | Stable public engine, caller-owned result types, typed errors, observers, and dependency-injection options |
| `cmd/kg2bb` | Process startup, signal handling, terminal detection, and build-version reporting |
| `internal/cli` | Command parsing, prompts, review flows, rendering, and exit-code mapping |
| `internal/service` | Use-case orchestration through small client and matcher interfaces |
| `internal/wiring` | Production construction and adapters between service interfaces and concrete clients |
| `internal/model` | I/O-free domain records and song/search normalization |
| `internal/matcher` | Candidate filtering, scoring, ranking, and selection thresholds |
| `internal/kugou` | Kugou playlist extraction and response parsing |
| `internal/bilibili` | Authentication, search, WBI signing, cookies, and favorite operations |
| `internal/browser` | Verified Chromium installation and dynamic-page extraction fallback |
| `internal/config` | State paths, embedded matcher defaults, and one-time legacy-state migration |
| `internal/netx` | Shared HTTP retry, concurrency, and rate-limit behavior |
| `internal/parity` | Cross-package compatibility tests against the captured Python behavior |

## Public and internal data

The root package returns public snapshots rather than exposing internal models
or site-client response types. Conversion at the public boundary is deliberate:
it lets internal representations evolve without silently changing the reusable
API. Slices and nested values returned by the engine are caller-owned.

`internal/service` defines interfaces at the point where they are consumed.
Concrete Kugou, Bilibili, browser, matcher, and storage implementations are
assembled only in `internal/wiring`. Tests can therefore replace external I/O
without routing through terminal code.

## Configuration ownership

Matcher defaults are source-controlled in `internal/config/defaults` and
embedded into the executable. Files named `b.txt`, `w.txt`, and `w-up.txt` in
the user configuration directory are optional complete overrides.

Root files with those names are treated only as legacy user state during the
one-time migration. They are intentionally ignored by Git and are not project
defaults.

## Test tiers

- `go test ./...` runs unit, fixture, boundary, and Python-parity tests without
  requiring authenticated remote writes.
- `go test -race ./...` validates concurrent matching, observers, clients, and
  injected dependencies.
- The `live` tag enables read-only network canaries.
- The `browser_install` tag validates a pinned Chromium archive, launch, and
  controlled extraction.
- The `authenticated` tag can create and remove temporary Bilibili resources;
  it additionally requires `KG2BB_RUN_AUTH_CANARY=1`.

## Extending the system

- Add terminal-only behavior to `internal/cli`.
- Add orchestration rules and dependency interfaces to `internal/service`.
- Add site protocol details to the corresponding integration package.
- Connect a new concrete implementation in `internal/wiring`.
- Change public types or methods only when the reusable API itself must change.
