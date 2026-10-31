//go:build android

package utils

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

//go:embed fontconfig_android.conf
var androidFontconfig []byte

// Android ships fonts but no Fontconfig configuration. Keep both configuration
// and the font index in the app's writable cache, shared by subtitle windows.
var androidFontconfigFile = sync.OnceValues(func() (string, error) {
	dir := filepath.Join(os.TempDir(), "fontconfig")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("androidFontconfigFile: %w", err)
	}
	path := filepath.Join(dir, "fonts.conf")
	if err := os.WriteFile(path, androidFontconfig, 0600); err != nil {
		return "", fmt.Errorf("androidFontconfigFile: %w", err)
	}
	return path, nil
})

func setSysProcAttr(cmd *exec.Cmd) {
	if os.Getenv("FONTCONFIG_FILE") != "" {
		return
	}
	path, err := androidFontconfigFile()
	if err != nil {
		// Fontconfig's compiled fallback still discovers /system/fonts.
		return
	}
	cmd.Env = append(cmd.Environ(), "FONTCONFIG_FILE="+path, "XDG_CACHE_HOME="+filepath.Dir(path))
}
