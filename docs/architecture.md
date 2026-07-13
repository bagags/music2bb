# Architecture

`music2bb` separates terminal interaction, application orchestration, neutral
playlist ingestion, and external-site protocols so the conversion engine can be
reused by another Go frontend.

## Dependency direction

```text
cmd/music2bb
├── internal/cli
│   └── music2bb (module root)
└── music2bb (module root)
    ├── internal/service
    ├── internal/wiring
    ├── internal/model
    ├── internal/config
    ├── internal/playlist
    ├── internal/bilibili
    └── internal/browser

internal/wiring
├── internal/service
├── internal/playlist
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
| `music2bb` | Stable public engine, caller-owned result types, typed errors, observers, and dependency-injection options |
| `cmd/music2bb` | Process startup, signal handling, terminal detection, and build-version reporting |
| `internal/cli` | Command parsing, prompts, review flows, rendering, and exit-code mapping |
| `internal/service` | Use-case orchestration through small client and matcher interfaces |
| `internal/wiring` | Production construction and adapters between service interfaces and concrete clients |
| `internal/playlist` | Provider identification, typed optimizations, candidate decoding, merging, and browser-fallback coordination |
| `internal/model` | I/O-free domain records and song/search normalization |
| `internal/matcher` | Candidate filtering, scoring, ranking, and selection thresholds |
| `internal/kugou` | Browser-free Kugou protocol optimization, response parsing, and song cleanup |
| `internal/bilibili` | Authentication, search, WBI signing, cookies, and favorite operations |
| `internal/browser` | Verified Chromium installation and provider-neutral dynamic-page candidate extraction |
| `internal/config` | State paths, embedded matcher defaults, and one-time legacy-state migration |
| `internal/netx` | Shared HTTP retry, concurrency, and rate-limit behavior |
| `internal/parity` | Cross-package compatibility tests against the captured Python behavior |

## Public and internal data

The root package returns public snapshots rather than exposing internal models
or site-client response types. Conversion at the public boundary is deliberate:
it lets internal representations evolve without silently changing the reusable
API. Slices and nested values returned by the engine are caller-owned.

`internal/service` defines interfaces at the point where they are consumed and
continues to receive decoded `model.Song` values. `internal/playlist` sits behind
that boundary; provider IDs, raw candidates, registries, and optimization
interfaces do not enter the public package or service API. Concrete Kugou,
Bilibili, browser, matcher, and storage implementations are assembled only in
`internal/wiring`. Tests can therefore replace external I/O without routing
through terminal code.

The public `ParsePlaylist` and `ParsePlaylistWithOptions` methods, browser-policy
types, `HTTPClients.Kugou`, and `WithBrowserExtractor` remain compatibility
boundaries. The injected public browser extractor still returns public `Song`
values; the root adapter converts them into canonical internal candidates.

## Playlist providers and registries

Provider identification and provider optimizations are deliberately separate.
Each engine receives two immutable registries assembled explicitly by
`internal/wiring`:

- The identification registry contains a `ProviderID` and a pure URL identifier.
- The optimization registry is keyed by `ProviderID` and contains optional,
  category-specific capability slices.

URL validation happens centrally before identification and accepts only HTTP(S)
URLs with a host. Exactly one matching identifier selects that provider, no
matches select the generic provider, and multiple matches are treated as an
internal registration error. Identifiers use parsed hostnames and boundary-safe
suffix matching; registration order never decides identity. A known provider may
intentionally have no optimizations, demonstrating that identity and capability
availability are independent. There is no global `init` registration.

## Capability composition

Optimizations use small typed interfaces with only a diagnostic `Name` in
common. Playlist extractors produce ordered raw track candidates and an optional
expected total. Title extractors interpret candidates, and song normalizers
transform decoded songs. This avoids a string-keyed capability map and avoids a
monolithic provider interface.

Playlist extractors run in registration order until a non-empty complete result
is found. A result is incomplete only when its declared total is greater than the
number of decoded songs; a non-empty result without a declared total is complete.
Provider title extractors run before the common field-and-visible-text extractor,
and the first valid title wins. All registered normalizers compose in order after
decoding, whether candidates came from a provider extractor or Chromium. Omitting
a capability category selects its common fallback.

When a provider result is empty or incomplete, the coordinator may merge it with
the browser result. Provider data takes precedence, and deduplication retains the
existing hash and trimmed title/artist semantics. Failed capability attempts
carry provider ID, capability category, optimization name, and their underlying
error for internal diagnostics; they do not create a new public error category.

## Browser fallback policy

Chromium is a coordinator-owned common fallback rather than a capability
implicitly attached to every provider.

| Policy | Processing |
|---|---|
| `never` | Run registered provider playlist optimizations only. An unoptimized provider returns an extraction error without launching or installing Chromium. |
| `auto` | Run provider optimizations, then use an injected or already-installed browser only for an empty or incomplete result. Preserve useful partial songs if the browser is unavailable or fallback fails. |
| `always` | Require an injected or verified installed browser before processing. Still run provider optimizations first and launch Chromium only for an empty or incomplete result. |

The backend never installs Chromium or prompts for approval. The CLI owns status,
approval, installation, and retry. Cancellation or deadline expiry always wins
over partial success; other fallback errors are ignored when usable songs exist
and are aggregated only when no songs can be returned. Malformed URLs,
cancellation, extraction failures, and missing required Chromium continue to map
to the existing public error categories and CLI exit codes.

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
  it additionally requires `MUSIC2BB_RUN_AUTH_CANARY=1`.

## Extending the system

- Add terminal-only behavior to `internal/cli`.
- Add orchestration rules and dependency interfaces to `internal/service`.
- Give a playlist source a stable internal `ProviderID` and a pure identifier.
- Keep site protocol details in the corresponding integration package and
  implement only the typed optimization categories it supports.
- Register the identifier and optimization set independently and explicitly in
  `internal/wiring`. A capability implementation may be reused under another
  provider ID.
- Add a future capability category by defining its typed input/output interface,
  adding its optional slice to the optimization set, and integrating it at the
  relevant ingestion stage; existing providers need no implementation changes.
- Change public types or methods only when the reusable API itself must change.

Provider detection is automatic and providers are compiled into the application.
Do not add a CLI provider switch, public registration API, runtime plugin ABI, or
global registration side effect.
