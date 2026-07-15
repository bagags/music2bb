# Third-Party Notices

This file records the copyright and license notices for third-party code
linked into or bundled with music2bb release binaries. The Go dependency
versions are those in `go.mod`; update this file when dependencies change.

## MIT-licensed components

- `charm.land/bubbles/v2` — Copyright (c) 2020-2026 Charmbracelet, Inc.
- `charm.land/bubbletea/v2` — Copyright (c) 2020-2026 Charmbracelet, Inc.
- `charm.land/lipgloss/v2` — Copyright (c) 2021-2026 Charmbracelet, Inc.
- `github.com/go-rod/rod` — Copyright 2019 Yad Smood
- `github.com/skip2/go-qrcode` — Copyright (c) 2014 Tom Harwood
- `github.com/charmbracelet/colorprofile` — Copyright (c) 2020-2024 Charmbracelet, Inc.
- `github.com/charmbracelet/ultraviolet` — Copyright (c) 2025 Charmbracelet, Inc.
- `github.com/charmbracelet/x/ansi` — Copyright (c) 2023 Charmbracelet, Inc.
- `github.com/charmbracelet/x/term` — Copyright (c) 2023 Charmbracelet, Inc.
- `github.com/charmbracelet/x/termios` — Copyright (c) 2023 Charmbracelet, Inc.
- `github.com/charmbracelet/x/windows` — Copyright (c) 2023 Charmbracelet, Inc.
- `github.com/clipperhouse/displaywidth` — Copyright (c) 2025 Matt Sherman
- `github.com/clipperhouse/uax29/v2` — Copyright (c) 2020 Matt Sherman
- `github.com/lucasb-eyer/go-colorful` — Copyright (c) 2013 Lucas Beyer
- `github.com/mattn/go-runewidth` — Copyright (c) 2016 Yasuhiro Matsumoto
- `github.com/muesli/cancelreader` — Copyright (c) 2022 Erik Geiser and Christian Muehlhaeuser
- `github.com/rivo/uniseg` — Copyright (c) 2019 Oliver Kuederle
- `github.com/xo/terminfo` — Copyright (c) 2016 Anmol Sethi
- `github.com/ysmood/fetchup` — Copyright (c) 2023 Yad Smood
- `github.com/ysmood/goob` — Copyright 2019 Yad Smood
- `github.com/ysmood/got` — Copyright 2020 Yad Smood
- `github.com/ysmood/gson` — Copyright 2020 Yad Smood
- `github.com/ysmood/leakless` — Copyright 2019 Yad Smood

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## BSD-3-Clause component

`github.com/atotto/clipboard` is Copyright (c) 2013 Ato Araki. All rights
reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

- Redistributions of source code must retain the above copyright notice,
  this list of conditions and the following disclaimer.
- Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.
- Neither the name of @atotto. nor the names of its contributors may be used to
  endorse or promote products derived from this software without specific
  prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.

## Go and BSD-licensed Go components

This notice applies to the Go runtime and standard library and to
`golang.org/x/sync`, `golang.org/x/sys`, and `golang.org/x/term`.

Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

- Redistributions of source code must retain the above copyright notice,
  this list of conditions and the following disclaimer.
- Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.
- Neither the name of Google LLC nor the names of its contributors may be
  used to endorse or promote products derived from this software without
  specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE
LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.

### Additional IP Rights Grant (Patents)

"This implementation" means the copyrightable works distributed by Google as
part of the Go project.

Google hereby grants to You a perpetual, worldwide, non-exclusive, no-charge,
royalty-free, irrevocable (except as stated in this section) patent license to
make, have made, use, offer to sell, sell, import, transfer and otherwise run,
modify and propagate the contents of this implementation of Go, where such
license applies only to those patent claims, both currently owned or
controlled by Google and acquired in the future, licensable by Google that
are necessarily infringed by this implementation of Go. This grant does not
include claims that would be infringed only as a consequence of further
modification of this implementation. If you or your agent or exclusive
licensee institute or order or agree to the institution of patent litigation
against any entity (including a cross-claim or counterclaim in a lawsuit)
alleging that this implementation of Go or any code incorporated within this
implementation of Go constitutes direct or contributory patent infringement,
or inducement of patent infringement, then any patent rights granted to you
under this License for this implementation of Go shall terminate as of the
date such litigation is filed.

## Chromium

Platform release binaries embed an unmodified, checksum-pinned Chromium
snapshot selected for the target platform: `152.0.7951.0` on macOS and Linux,
and the newest available Windows build, `152.0.7950.0`, at the recorded cutoff.
Exact commit, snapshot, archive, publication, and SHA-256 provenance is recorded in
`CHROMIUM_PROVENANCE.md` and `CHROMIUM_PROVENANCE.json`. Chromium is Copyright
2015 The Chromium Authors and is distributed under the following BSD-style
license:

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

- Redistributions of source code must retain the above copyright notice,
  this list of conditions and the following disclaimer.
- Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.
- Neither the name of Google LLC nor the names of its contributors may be
  used to endorse or promote products derived from this software without
  specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER OR CONTRIBUTORS BE
LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.

Chromium also contains third-party components under their own licenses. Every
music2bb release package that embeds Chromium includes the complete
`CHROMIUM_CREDITS.html` generated from Chromium's shipped-component license
metadata and audited through the newest embedded source revision. Snapshot
builds contain only Chromium's sample `chrome://credits` resource, so that
placeholder is not used as the release notice. The credits file's exact source,
delta audit, and SHA-256 are recorded in `CHROMIUM_PROVENANCE.md` and
`CHROMIUM_PROVENANCE.json`.
