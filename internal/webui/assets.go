package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
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

// assetsHash identifies the embedded UI build; clients compare it across
// reconnects to decide between a plain reconnect and a full page reload.
var assetsHash = func() string {
	digest := sha256.New()
	err := fs.WalkDir(assets, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		digest.Write([]byte(path))
		digest.Write([]byte{0})
		digest.Write(data)
		return nil
	})
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(digest.Sum(nil))[:16]
}()
