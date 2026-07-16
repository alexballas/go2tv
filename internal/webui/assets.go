package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embedded embed.FS

var assets = func() fs.FS {
	result, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return result
}()
