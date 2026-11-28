//go:build android || ios

package gui

import (
	"bytes"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
)

func copyDiagnosticsToClipboard(s *FyneScreen) {
	if s == nil || s.Current == nil {
		return
	}

	var buf bytes.Buffer
	if err := writeDiagnostics(&buf, s); err != nil {
		dialog.ShowError(err, s.Current)
		return
	}

	if app := fyne.CurrentApp(); app != nil {
		app.Clipboard().SetContent(buf.String())
	}

	dialog.ShowInformation(lang.L("Diagnostics"), lang.L("Copied to clipboard"), s.Current)
}

func showPendingCrashPopup(*FyneScreen) {}
