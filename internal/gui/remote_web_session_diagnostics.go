//go:build !(android || ios)

package gui

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	fyne "github.com/alexballas/refyne/v2"
	fynedialog "github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	xfilepicker "github.com/alexballas/xfilepicker/dialog"
)

func remoteDiagnosticsFileName() string {
	return "go2tv-remote-session-diagnostics.txt"
}

func writeRemoteSessionDiagnostics(w io.Writer, version string, snapshot remoteSessionSnapshot) error {
	if _, err := fmt.Fprintf(
		w,
		"Go2TV Remote Web Session Diagnostics\nGenerated: %s\nVersion: %s\nGOOS/GOARCH: %s/%s\nGo version: %s\nState: %s\n",
		time.Now().UTC().Format(time.RFC3339),
		version,
		runtime.GOOS,
		runtime.GOARCH,
		runtime.Version(),
		snapshot.State,
	); err != nil {
		return err
	}
	if snapshot.URL != "" {
		if _, err := fmt.Fprintf(w, "URL: %s\n", snapshot.URL); err != nil {
			return err
		}
	}
	if snapshot.LastError != "" {
		if _, err := fmt.Fprintf(w, "Last error: %s\n", snapshot.LastError); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n=== Server Log Ring ===\n"); err != nil {
		return err
	}
	if len(snapshot.Logs) == 0 {
		_, err := io.WriteString(w, "Server logs are empty.\n")
		return err
	}
	for _, line := range snapshot.Logs {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func showRemoteDiagnosticsSaveDialog(s *FyneScreen, parent fyne.Window) {
	var resumeHotkeys func()
	fd := xfilepicker.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if resumeHotkeys != nil {
			defer resumeHotkeys()
		}
		if err != nil {
			fynedialog.ShowError(err, parent)
			return
		}
		if writer == nil {
			return
		}
		saveRemoteSessionDiagnostics(writer, s, parent)
	}, parent)

	if picker, ok := fd.(interface{ SetFileName(string) }); ok {
		picker.SetFileName(remoteDiagnosticsFileName())
	}
	if picker, ok := fd.(xfilepicker.FilePicker); ok {
		cwd, err := os.Getwd()
		if err == nil {
			if lister, listerErr := storage.ListerForURI(storage.NewFileURI(cwd)); listerErr == nil {
				picker.SetLocation(lister)
			}
		}
	}

	resumeHotkeys = suspendHotkeys(s)
	showFilePicker(fd, parent)
}

func saveRemoteSessionDiagnostics(writer fyne.URIWriteCloser, s *FyneScreen, parent fyne.Window) {
	defer writer.Close()
	if err := writeRemoteSessionDiagnostics(writer, s.version, s.remoteSession.Snapshot()); err != nil {
		fynedialog.ShowError(err, parent)
		return
	}
	fynedialog.ShowInformation(lang.L("Diagnostics"), lang.L("Saved to")+"... "+writer.URI().String(), parent)
}
