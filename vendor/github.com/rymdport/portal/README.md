[![Go API Reference](https://img.shields.io/badge/go-documentation-blue.svg?style=flat)](https://pkg.go.dev/github.com/rymdport/portal)
[![Tests](https://github.com/rymdport/portal/actions/workflows/tests.yml/badge.svg)](https://github.com/rymdport/portal/actions/workflows/tests.yml)
[![Analysis](https://github.com/rymdport/portal/actions/workflows/analysis.yml/badge.svg)](https://github.com/rymdport/portal/actions/workflows/analysis.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/rymdport/portal)](https://goreportcard.com/report/github.com/rymdport/portal)

# Portal

Portal is a Go wrapper around the [XDG Desktop Protocol](https://flatpak.github.io/xdg-desktop-portal/) D-Bus API.
This allows the code to request information from the user through the help of the desktop environment even when running inside a sandbox like Flatpak.  
As such, it is possible to easily open file dialogs, links and send notifications to the user in a way that integrates well with the desktop environment.

When running inside a sandbox, this runtime request of permissions allows the code to do various things without having to grant more permissions to the sandbox.
However, the portal APIs should still also work good even when used outside of a sandboxed environment.

The goal of this project is to be a toolkit agnostic package for accessing these interfaces through Go code without needing to access [libportal](https://github.com/flatpak/libportal) through CGo.

## API

The api of this Go module is designed to closely follow the structure naming of the upstream APIs. This means, in practice, that each D-Bus interface is implemented as its own package here.
As such, it is also very useful to look at the API Reference for the protocol: https://flatpak.github.io/xdg-desktop-portal/docs/api-reference.html

Documentation for this module and all of its packages can be found on pkg.go.dev: https://pkg.go.dev/github.com/rymdport/portal

The version of this module's API is still in a `v0.X.Y` state and is subject to change in the future.
A release with breaking changes will increment X while Y will be incremented when there are minor bug or feature improvements.

## Example

The following example showcases how a file chooser can be opened for selecting one or more files.


```go
package main

import (
	"fmt"
	"log"

	"github.com/rymdport/portal/filechooser"
)

func main() {
	options := filechooser.OpenFileOptions{Multiple: true}
	files, err := filechooser.OpenFile("", "Select files", &options)
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Println(files)
}
```

## Supported Portal APIs

The list below contains all of the portal APIs available as of 2024-03-14. Checked boxes are partially or completely implemented within this project.

- [x] Account
- [x] Background
- [ ] Camera
- [ ] Clipboard
- [ ] Device
- [ ] Documents
- [ ] Dynamic Launcher
- [ ] Email
- [x] File Chooser
- [ ] File Transfer
- [ ] Game Mode
- [ ] Global Shortcuts
- [ ] Inhibit
- [ ] Input Capture
- [ ] Location
- [ ] Memory Monitor
- [ ] Network Monitor
- [x] Notification
- [x] OpenURI
- [ ] Power Profile Monitor
- [ ] Print
- [ ] Proxy Resolver
- [ ] Realtime
- [ ] Remote Desktop
- [ ] Request
- [ ] ScreenCast
- [ ] Screenshot
- [ ] Secret
- [ ] Session
- [x] Settings
- [ ] Trash
- [ ] Wallpaper


## Used by other projects

This section is meant as a reference to where this project is being used. Feel free to add yours if desired.

- This project is used as of the [v2.5.0](https://github.com/fyne-io/fyne/releases/tag/v2.5.0) release of [Fyne](https://fyne.io).
  - All the old theme watching code has been replaced by the `settings` package (and `appearance` subpackage) from this module. The `filechooser` and `notification` packages replace the old Fyne-code when compiling with `-tags flatpak`.

## Contributing

Contributions are strongly appreciated. Everything from creating bug reports to contributing code will help the project a lot, so please feel free to help in any way, shape, or form that you feel comfortable with.

## License
- Portal is licensed under `Apache License Version 2.0` and will forever continue to be free and open source.
