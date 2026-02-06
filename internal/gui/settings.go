//go:build !(android || ios)

package gui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynedialog "fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	xfilepicker "github.com/alexballas/xfilepicker/dialog"
	"go2tv.app/go2tv/v2/rtmp"
)

type numericalEntry struct {
	widget.Entry
}

func newNumericalEntry() *numericalEntry {
	e := &numericalEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *numericalEntry) TypedRune(r rune) {
	if r >= '0' && r <= '9' {
		e.Entry.TypedRune(r)
	}
}

func settingsWindow(s *FyneScreen) fyne.CanvasObject {

	w := s.Current

	themeText := widget.NewLabel(lang.L("Theme"))
	dropdownTheme := widget.NewSelect([]string{lang.L("System Default"), lang.L("Light"), lang.L("Dark")}, parseTheme(s))

	languageText := widget.NewLabel(lang.L("Language"))
	dropdownLanguage := widget.NewSelect([]string{lang.L("System Default"), "English", "中文(简体)"}, parseLanguage(s))
	selectedLanguage := fyne.CurrentApp().Preferences().StringWithFallback("Language", "System Default")

	if selectedLanguage == "System Default" {
		selectedLanguage = lang.L("System Default")
	}

	dropdownLanguage.PlaceHolder = selectedLanguage

	themeName := lang.L(fyne.CurrentApp().Preferences().StringWithFallback("Theme", "System Default"))
	dropdownTheme.PlaceHolder = themeName
	parseTheme(s)(themeName)

	s.systemTheme = fyne.CurrentApp().Settings().ThemeVariant()

	ffmpegText := widget.NewLabel("ffmpeg " + lang.L("Path"))
	ffmpegTextEntry := widget.NewEntry()

	ffmpegFolderReset := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		path, err := exec.LookPath("ffmpeg")
		ffmpegTextEntry.SetText(path)
		if err != nil {
			ffmpegTextEntry.SetText("ffmpeg")
		}
		s.ffmpegPath = ffmpegTextEntry.Text
	})

	ffmpegFolderSelect := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		var resumeHotkeys func()
		xfilepicker.SetFFmpegPath(s.ffmpegPath)
		fd := xfilepicker.NewFolderOpen(func(lu fyne.ListableURI, err error) {
			if resumeHotkeys != nil {
				defer resumeHotkeys()
			}
			if err != nil {
				fynedialog.ShowError(err, w)
				return
			}
			if lu == nil {
				return
			}

			p := filepath.ToSlash(lu.Path() + string(filepath.Separator) + "ffmpeg")
			ffmpegTextEntry.SetText(p)
		}, w)

		if f, ok := fd.(xfilepicker.FilePicker); ok {
			ffmpegURI := storage.NewFileURI(filepath.Dir(s.ffmpegPath))
			ffmpegLister, err := storage.ListerForURI(ffmpegURI)
			if err == nil {
				f.SetLocation(ffmpegLister)
			}
		}

		resumeHotkeys = suspendHotkeys(s)
		fd.Show()
		fd.Resize(fyne.NewSize(filePickerFillSize, filePickerFillSize))

	})

	ffmpegRightButtons := container.NewHBox(ffmpegFolderSelect, ffmpegFolderReset)
	ffmpegPathControls := container.New(layout.NewBorderLayout(nil, nil, nil, ffmpegRightButtons), ffmpegRightButtons, ffmpegTextEntry)

	ffmpegTextEntry.Text = s.ffmpegPath
	ffmpegTextEntry.Refresh()

	s.ffmpegPath = ffmpegTextEntry.Text

	ffmpegTextEntry.OnChanged = func(update string) {
		s.ffmpegPath = update
		fyne.CurrentApp().Preferences().SetString("ffmpeg", update)
		s.ffmpegPathChanged = true
	}

	debugText := widget.NewLabel(lang.L("Debug"))
	debugExport := widget.NewButton(lang.L("Export Debug Logs"), func() {
		var itemInRing bool
		s.Debug.ring.Do(func(p any) {
			if p != nil {
				itemInRing = true
			}
		})

		if !itemInRing {
			fynedialog.ShowInformation(lang.L("Debug"), lang.L("Debug logs are empty"), w)
			return
		}

		var resumeHotkeys func()
		fd := xfilepicker.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
			if resumeHotkeys != nil {
				defer resumeHotkeys()
			}
			if err != nil {
				fynedialog.ShowError(err, s.Current)
				return
			}
			if writer == nil {
				return
			}

			saveDebugLogs(writer, s)
		}, s.Current)

		if f, ok := fd.(interface{ SetFileName(string) }); ok {
			f.SetFileName("go2tv-debug.log")
		}

		if f, ok := fd.(xfilepicker.FilePicker); ok {
			cwd, err := os.Getwd()
			if err == nil {
				if lister, listerErr := storage.ListerForURI(storage.NewFileURI(cwd)); listerErr == nil {
					f.SetLocation(lister)
				}
			}
		}

		resumeHotkeys = suspendHotkeys(s)
		fd.Show()
		fd.Resize(fyne.NewSize(filePickerFillSize, filePickerFillSize))
	})

	gaplessText := widget.NewLabel(lang.L("Gapless Playback"))
	gaplessdropdown := widget.NewSelect([]string{lang.L("Enabled"), lang.L("Disabled")}, func(ss string) {
		var selection string
		if lang.L("Enabled") == ss {
			selection = "Enabled"
		}

		if lang.L("Disabled") == ss {
			selection = "Disabled"
		}

		if selection == "Enabled" && fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled") == "Disabled" {
			fynedialog.ShowInformation(lang.L("Gapless Playback"), lang.L(`Some devices don't support gapless playback. If 'Auto-Play Next File' isn't working properly, try turning it off.`), w)
		}

		fyne.CurrentApp().Preferences().SetString("Gapless", selection)
		if s.NextMediaCheck.Checked {
			switch selection {
			case "Enabled":
				switch s.State {
				case "Playing", "Paused":
					newTVPayload, err := queueNext(s, false)
					if err == nil && s.GaplessMediaWatcher == nil {
						s.GaplessMediaWatcher = gaplessMediaWatcher
						go s.GaplessMediaWatcher(s.serverStopCTX, s, newTVPayload)
					}
				}
			case "Disabled":
				// We're disabling gapless playback. If for some reason
				// we fail to clear the NextURI it would be best to stop and
				// avoid inconsistencies where gapless playback appears disabled
				// but in reality it's not.
				_, err := queueNext(s, true)
				if err != nil {
					stopAction(s)
				}
			}
		}
	})
	gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
	gaplessdropdown.SetSelected(lang.L(gaplessOption))

	dropdownTheme.Refresh()

	sameTypeAutoNext := widget.NewLabel(lang.L("Auto-Play"))
	sameTypeAutoNextCheck := widget.NewCheck(lang.L("Same File Types Only"), func(b bool) {
		fyne.CurrentApp().Preferences().SetBool("AutoPlaySameTypes", b)
		s.SkinNextOnlySameTypes = b
	})

	sameTypeAutoNextOption := fyne.CurrentApp().Preferences().BoolWithFallback("AutoPlaySameTypes", true)
	sameTypeAutoNextCheck.SetChecked(sameTypeAutoNextOption)

	rtmpPortLabel := widget.NewLabel(lang.L("RTMP Port"))
	rtmpPortEntry := newNumericalEntry()
	rtmpPortEntry.Text = fyne.CurrentApp().Preferences().StringWithFallback("RTMPPort", "1935")
	rtmpPortEntry.Validator = func(s string) error {
		port, err := strconv.Atoi(s)
		if err != nil {
			return errors.New("invalid port")
		}
		if port < 1024 || port > 65535 {
			return errors.New("port out of range (1024-65535)")
		}
		return nil
	}
	rtmpPortEntry.OnChanged = func(s string) {
		if err := rtmpPortEntry.Validate(); err == nil {
			fyne.CurrentApp().Preferences().SetString("RTMPPort", s)
		}
	}

	rtmpKeyLabel := widget.NewLabel(lang.L("RTMP Stream Key"))
	streamKeyEntry := widget.NewEntry()
	streamKeyEntry.Password = true
	streamKeyEntry.Text = fyne.CurrentApp().Preferences().StringWithFallback("RTMPStreamKey", "")
	if streamKeyEntry.Text == "" {
		streamKeyEntry.Text = rtmp.GenerateKey()
		fyne.CurrentApp().Preferences().SetString("RTMPStreamKey", streamKeyEntry.Text)
	}
	streamKeyEntry.OnChanged = func(s string) {
		fyne.CurrentApp().Preferences().SetString("RTMPStreamKey", s)
	}

	regenKeyBtn := widget.NewButton(lang.L("Regenerate"), func() {
		newKey := rtmp.GenerateKey()
		streamKeyEntry.SetText(newKey)
		fyne.CurrentApp().Preferences().SetString("RTMPStreamKey", newKey)
	})

	var toggleKeyBtn *widget.Button
	toggleKeyBtn = widget.NewButtonWithIcon("", theme.VisibilityIcon(), func() {
		if streamKeyEntry.Password {
			streamKeyEntry.Password = false
			toggleKeyBtn.SetIcon(theme.VisibilityOffIcon())
		} else {
			streamKeyEntry.Password = true
			toggleKeyBtn.SetIcon(theme.VisibilityIcon())
		}
		streamKeyEntry.Refresh()
	})

	rtmpKeyContainer := container.NewBorder(nil, nil, nil, container.NewHBox(toggleKeyBtn, regenKeyBtn), streamKeyEntry)

	return container.New(layout.NewFormLayout(), themeText, dropdownTheme, languageText, dropdownLanguage, gaplessText, gaplessdropdown, ffmpegText, ffmpegPathControls, sameTypeAutoNext, sameTypeAutoNextCheck, debugText, debugExport, rtmpPortLabel, rtmpPortEntry, rtmpKeyLabel, rtmpKeyContainer)
}

func saveDebugLogs(f fyne.URIWriteCloser, s *FyneScreen) {
	w := s.Current
	defer f.Close()

	s.Debug.ring.Do(func(p any) {
		if p != nil {
			_, err := f.Write([]byte(p.(string)))
			if err != nil {
				fynedialog.ShowError(err, w)
			}
		}
	})
	fynedialog.ShowInformation(lang.L("Debug"), lang.L("Saved to")+"... "+f.URI().String(), w)
}

func parseTheme(s *FyneScreen) func(string) {
	return func(t string) {
		switch t {
		case lang.L("Light"):
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Light"})
			fyne.CurrentApp().Preferences().SetString("Theme", "Light")
		case lang.L("Dark"):
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Dark"})
			fyne.CurrentApp().Preferences().SetString("Theme", "Dark")
		default:
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"System Default"})
			fyne.CurrentApp().Preferences().SetString("Theme", "System Default")
		}
		if s != nil {
			if s.Current != nil {
				s.Current.Content().Refresh()
			}
			if s.NextMediaCheck != nil {
				s.NextMediaCheck.Refresh()
			}
			if s.LoopSelectedCheck != nil {
				s.LoopSelectedCheck.Refresh()
			}
			if s.TranscodeCheckBox != nil {
				s.TranscodeCheckBox.Refresh()
			}
			if s.ExternalMediaURL != nil {
				s.ExternalMediaURL.Refresh()
			}
			if s.CustomSubsCheck != nil {
				s.CustomSubsCheck.Refresh()
			}
		}
	}
}

func parseLanguage(s *FyneScreen) func(string) {
	w := s.Current
	return func(t string) {
		if t != fyne.CurrentApp().Preferences().StringWithFallback("Language", "System Default") {
			fynedialog.ShowInformation(lang.L("Update Language Preferences"), lang.L(`Please restart the application for the changes to take effect.`), w)
		}
		go func() {
			switch t {
			case "English":
				fyne.CurrentApp().Preferences().SetString("Language", "English")
			case "中文(简体)":
				fyne.CurrentApp().Preferences().SetString("Language", "中文(简体)")
			default:
				fyne.CurrentApp().Preferences().SetString("Language", "System Default")
			}
		}()
	}
}
