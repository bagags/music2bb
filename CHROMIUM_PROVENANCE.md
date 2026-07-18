# Chromium Provenance

music2bb release binaries do not contain Chromium. At runtime they prefer an
explicitly selected or discovered system Chromium/Google Chrome and otherwise
download a platform-pinned managed archive. Managed archives are verified before
extraction, and their executables are reverified before launch. This record was
resolved from Chromium's official snapshot service on 2026-07-14 at 15:02:53 UTC.

Chromium's builders publish different commit positions asynchronously for each
platform. The five upstream archives below were the newest objects advertised by each
platform's official `LAST_CHANGE` endpoint at resolution time. macOS and Linux
had advanced to Chromium `152.0.7951.0`; the newest Windows builders still
reported `152.0.7950.0`. Versions come from `chrome/VERSION` at each exact
source commit.

## Exact artifacts

### macOS arm64

- Version: `152.0.7951.0`
- Commit position: `refs/heads/main@{#1661829}`
- Chromium commit: `544d3cf2b1f8195b3133e90511d95e8c0325569b`
- Archive generation: `1784040642782610`
- Published: `2026-07-14T14:50:42.845Z`
- Archive bytes: `168652681`
- SHA-256: `dba25ecc9fcc9dba2c9929c238b732a36411fa82e3741de28f6a10fc0eae98bf`
- Archive: <https://storage.googleapis.com/chromium-browser-snapshots/Mac_Arm/1661829/chrome-mac.zip?generation=1784040642782610>
- Revision record: <https://storage.googleapis.com/chromium-browser-snapshots/Mac_Arm/1661829/REVISIONS>
- Source: <https://chromium.googlesource.com/chromium/src/+/544d3cf2b1f8195b3133e90511d95e8c0325569b>
- License: <https://chromium.googlesource.com/chromium/src/+/544d3cf2b1f8195b3133e90511d95e8c0325569b/LICENSE>

### macOS amd64

- Version: `152.0.7951.0`
- Commit position: `refs/heads/main@{#1661832}`
- Chromium commit: `45ff7c7b210b9e49d12881193cbb2fbd6b78457e`
- Archive generation: `1784041116228814`
- Published: `2026-07-14T14:58:36.292Z`
- Archive bytes: `187141912`
- SHA-256: `7b396529a1f55adb5a7817eb7327a1231e049cbbd52e5b31456c0aa9df5e029f`
- Archive: <https://storage.googleapis.com/chromium-browser-snapshots/Mac/1661832/chrome-mac.zip?generation=1784041116228814>
- Revision record: <https://storage.googleapis.com/chromium-browser-snapshots/Mac/1661832/REVISIONS>
- Source: <https://chromium.googlesource.com/chromium/src/+/45ff7c7b210b9e49d12881193cbb2fbd6b78457e>
- License: <https://chromium.googlesource.com/chromium/src/+/45ff7c7b210b9e49d12881193cbb2fbd6b78457e/LICENSE>

### Windows amd64

- Version: `152.0.7950.0`
- Commit position: `refs/heads/main@{#1661781}`
- Chromium commit: `45ce9117c5429ebf83502ff993056fc6e0d8575a`
- Archive generation: `1784037828099518`
- Published: `2026-07-14T14:03:48.157Z`
- Archive bytes: `344108432`
- SHA-256: `544983097cd287be0f43ad1df8bae80be468ae12d8ebd83e581e449b4d4d9b03`
- Archive: <https://storage.googleapis.com/chromium-browser-snapshots/Win_x64/1661781/chrome-win.zip?generation=1784037828099518>
- Revision record: <https://storage.googleapis.com/chromium-browser-snapshots/Win_x64/1661781/REVISIONS>
- Source: <https://chromium.googlesource.com/chromium/src/+/45ce9117c5429ebf83502ff993056fc6e0d8575a>
- License: <https://chromium.googlesource.com/chromium/src/+/45ce9117c5429ebf83502ff993056fc6e0d8575a/LICENSE>

### Windows arm64

- Version: `152.0.7950.0`
- Commit position: `refs/heads/main@{#1661807}`
- Chromium commit: `7e338d1ba16c391b2630f8431efabec5f469d7ec`
- Archive generation: `1784040869273457`
- Published: `2026-07-14T14:54:29.328Z`
- Archive bytes: `323001077`
- SHA-256: `0eec2e412daa206a5bb255cf03539354067896ab39611ee5abfa5e056c711a06`
- Archive: <https://storage.googleapis.com/chromium-browser-snapshots/Win_Arm64/1661807/chrome-win.zip?generation=1784040869273457>
- Revision record: <https://storage.googleapis.com/chromium-browser-snapshots/Win_Arm64/1661807/REVISIONS>
- Source: <https://chromium.googlesource.com/chromium/src/+/7e338d1ba16c391b2630f8431efabec5f469d7ec>
- License: <https://chromium.googlesource.com/chromium/src/+/7e338d1ba16c391b2630f8431efabec5f469d7ec/LICENSE>

### Linux amd64

This archive is downloaded at runtime on Linux; release packages do not embed it.

- Version: `152.0.7951.0`
- Commit position: `refs/heads/main@{#1661846}`
- Chromium commit: `24394684caab9de1b7f4a3a2f83636e3d1449b56`
- Archive generation: `1784040561659665`
- Published: `2026-07-14T14:49:21.738Z`
- Archive bytes: `240249260`
- SHA-256: `ba645202abec2a4b26b64442b64fd1c4b1833ad51cf8aaa6b67202a50dd06042`
- Archive: <https://storage.googleapis.com/chromium-browser-snapshots/Linux_x64/1661846/chrome-linux.zip?generation=1784040561659665>
- Revision record: <https://storage.googleapis.com/chromium-browser-snapshots/Linux_x64/1661846/REVISIONS>
- Source: <https://chromium.googlesource.com/chromium/src/+/24394684caab9de1b7f4a3a2f83636e3d1449b56>
- License: <https://chromium.googlesource.com/chromium/src/+/24394684caab9de1b7f4a3a2f83636e3d1449b56/LICENSE>

### Linux arm64

Linux ARM64 has no current official desktop snapshot in the upstream set. The
project builder uses the Linux AMD64 pin's exact Chromium version, revision, and
source commit, a pinned `depot_tools` commit, fixed GN arguments, and a
deterministic `chrome-linux/` ZIP recipe. It generates the Chromium BSD license,
complete third-party credits, and build metadata from that checkout, smoke-tests
the archive on `ubuntu-24.04-arm`, and publishes checksum and GitHub artifact
attestation assets under `chromium-152.0.7951.0-r1661846` without overwriting an
existing release.

- Origin: project build (publication pending)
- Version: `152.0.7951.0`
- Commit position: `refs/heads/main@{#1661846}`
- Chromium commit: `24394684caab9de1b7f4a3a2f83636e3d1449b56`
- `depot_tools` commit: `1709e20d5ca2afe97bfa726168719c3b77eb9883`
- Planned archive: <https://github.com/bagags/music2bb-go/releases/download/chromium-152.0.7951.0-r1661846/chromium-linux-arm64.zip>
- Source: <https://chromium.googlesource.com/chromium/src/+/24394684caab9de1b7f4a3a2f83636e3d1449b56>
- License: <https://chromium.googlesource.com/chromium/src/+/24394684caab9de1b7f4a3a2f83636e3d1449b56/LICENSE>
- Build recipe: [`.github/workflows/chromium-linux-arm64.yml`](.github/workflows/chromium-linux-arm64.yml)

The embedded manifest marks this artifact `pending-build` until the workflow has
published the immutable asset and its actual SHA-256, size, publication time,
builder run, and attestation URLs have replaced the pending fields. A music2bb
release is blocked while any target remains pending.

## Release compliance files

Every music2bb release package contains these transparency files even though it
contains no Chromium binary or archive:

- `THIRD_PARTY_NOTICES.md`, including Chromium's BSD license notice;
- `CHROMIUM_CREDITS.html`, the complete, checksum-pinned Chromium third-party
  credits described below;
- `CHROMIUM_PROVENANCE.md`, this human-readable record;
- `CHROMIUM_PROVENANCE.json`, the machine-readable manifest embedded in the
  music2bb binary; and
- `SOURCE.txt`, identifying the exact music2bb release source.

The snapshot builders set Chromium's `generate_about_credits` build argument
to false, so their built-in `chrome://credits` page is an explicit sample and
cannot be redistributed as complete notice text. The checked-in credits file
therefore starts with the generated resource from the newest official desktop
build that had complete credits at resolution time:

- Credits base: Chrome for Testing `152.0.7949.0` (Official Build, arm64)
- Reported Chromium revision:
  `a735cab71118205a8bed005264dd415fb9b544db-refs/branch-heads/7949@{#1}`
- Main commit position: `refs/heads/main@{#1661480}`
- Main commit: `872a849b602954937d7b4ec19cd353f8351b5468`
- Archive generation: `1784015902449414`
- Published: `2026-07-14T07:58:22Z`
- Archive bytes: `187781583`
- Archive SHA-256:
  `43ed7dcebbdf54728bcae5af09f6098f12aa678d7b1e5fafb0ac198c585028bc`
- Archive: <https://storage.googleapis.com/chrome-for-testing-public/152.0.7949.0/mac-arm64/chrome-mac-arm64.zip?generation=1784015902449414>
- Source: <https://chromium.googlesource.com/chromium/src/+/a735cab71118205a8bed005264dd415fb9b544db>

Chromium generates that resource with `tools/licenses/licenses.py` from
dependencies marked `Shipped: yes`. The source delta from main revision
`1661480` through the newest managed revision `1661846` was then audited for
`README.chromium`, license, notice, and dependency changes. Existing shipped
entries changed only revision or download URL. Root license texts for every
rolled shipped source dependency were compared at both ends and were
byte-identical: ANGLE, Anonymous Tokens, compiler-rt, Dawn, DevTools Frontend,
glslang, llvm-libc, Perfetto, Skia, SPIR-V Tools, V8, Vulkan Loader, Vulkan
Validation Layers, and WebRTC. Three newly shipped Rust crates were added to
the credits from the exact revision `1661846` source metadata and Apache-2.0
license text:

- `incremental-font-transfer` `0.5.0`;
- `skera` `0.4.0`; and
- `write-fonts` `0.50.0`.

The resulting `CHROMIUM_CREDITS.html` contains 767 project entries and has
SHA-256
`7bc7529cdf5389d377b993411c2e028d097b784432b79ea55a13317658a74ce5`.
The manifest records this generation and audit chain. Unit tests and release
builds verify the file hash, minimum entry count, required notices, and absence
of Chromium's sample page before packaging.

Each artifact entry links the Chromium license at its exact source commit. Do
not edit a snapshot archive or `CHROMIUM_CREDITS.html` without resolving and
recording a new artifact, regenerating the credits, and updating the hashes.
