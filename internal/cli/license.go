package cli

import "fmt"

const licenseNotice = `music2bb
Copyright (C) 2026 Chaoyi Liu, bagags, and music2bb contributors.

music2bb is free software: you can redistribute it and/or modify it under the
terms of the GNU General Public License as published by the Free Software
Foundation, version 3 of the License (GPL-3.0-only).

music2bb is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR
A PARTICULAR PURPOSE. See the GNU General Public License for more details.

License: https://github.com/bagags/music2bb-go/blob/main/LICENSE.md
Source: https://github.com/bagags/music2bb-go
`

func (a *App) printLicense() {
	fmt.Fprint(a.IO.Out, licenseNotice)
}
