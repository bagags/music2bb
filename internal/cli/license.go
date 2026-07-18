package cli

import "fmt"

const licenseNotice = `music2bb
Copyright (C) 2026 bagags and music2bb contributors.

This project is an independent continuation of earlier work by Chaoyi Liu,
under the GPL-3.0-only license.

music2bb is free software: you can redistribute it and/or modify it under the
terms of the GNU General Public License as published by the Free Software
Foundation, version 3 of the License (GPL-3.0-only).

music2bb is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR
A PARTICULAR PURPOSE. See the GNU General Public License for more details.

License: https://github.com/bagags/music2bb-go/blob/main/LICENSE.md
Source: https://github.com/bagags/music2bb-go

Release packages do not embed Chromium. They include THIRD_PARTY_NOTICES.md,
CHROMIUM_CREDITS.html, CHROMIUM_PROVENANCE.md, and CHROMIUM_PROVENANCE.json.
Chromium source and license: https://chromium.googlesource.com/chromium/src/
`

func (a *App) printLicense() {
	fmt.Fprint(a.IO.Out, licenseNotice)
}
