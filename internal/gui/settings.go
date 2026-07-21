//go:build !(android || ios)

package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	fynedialog "github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	xfilepicker "github.com/alexballas/xfilepicker/dialog"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/rtmp"
	"go2tv.app/go2tv/v2/utils"
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

func ffmpegDirDisplayPath(pref string) string {
	pref = strings.TrimSpace(pref)
	if pref == "" {
		path, err := utils.ResolveFFmpegPath("")
		if err != nil {
			return ""
		}
		return filepath.ToSlash(filepath.Dir(path))
	}

	if info, err := os.Stat(pref); err == nil {
		if info.IsDir() {
			return filepath.ToSlash(pref)
		}

		return filepath.ToSlash(filepath.Dir(pref))
	}

	if filepath.Base(pref) != pref {
		return filepath.ToSlash(filepath.Dir(pref))
	}

	path, err := utils.ResolveFFmpegPath(pref)
	if err != nil {
		return ""
	}

	return filepath.ToSlash(filepath.Dir(path))
}

func newSettingsField(label string, control fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(
		widget.NewLabel(label),
		container.NewPadded(control),
	)
}

func newSettingsCheckboxField(control fyne.CanvasObject) fyne.CanvasObject {
	return container.NewPadded(control)
}

func (s *FyneScreen) setAutoPlaySameTypes(enabled bool) {
	fyne.CurrentApp().Preferences().SetBool("AutoPlaySameTypes", enabled)
	s.SkinNextOnlySameTypes = enabled
	s.refreshTraversalControls()
}

func settingsWindow(s *FyneScreen) fyne.CanvasObject {
	w := s.Current

	dropdownTheme := widget.NewSelect([]string{lang.L("System Default"), lang.L("Light"), lang.L("Dark")}, parseTheme(s))

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

	ffmpegTextEntry := widget.NewEntry()
	var updatingFFmpegEntry bool

	rememberPlaybackPositionCheck := widget.NewCheck(lang.L("Remember Playback Position"), func(enabled bool) {
		fyne.CurrentApp().Preferences().SetBool(rememberPlaybackPositionPref, enabled)
	})
	rememberPlaybackPositionCheck.SetChecked(
		fyne.CurrentApp().Preferences().BoolWithFallback(rememberPlaybackPositionPref, false),
	)
	clearPlaybackHistoryButton := widget.NewButtonWithIcon(lang.L("Clear Playback History"), theme.DeleteIcon(), func() {
		store := currentResumeStore()
		if store == nil {
			return
		}

		if err := store.clear(); err != nil {
			fynedialog.ShowError(err, w)
			return
		}

		s.clearResumeSession()
		fynedialog.ShowInformation(lang.L("Playback History"), lang.L("Playback history cleared"), w)
	})
	rememberPlaybackPositionControls := container.NewGridWithColumns(2, rememberPlaybackPositionCheck, clearPlaybackHistoryButton)

	ffmpegFolderReset := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyne.CurrentApp().Preferences().SetString("ffmpeg", "")
		path := ffmpegDirDisplayPath("")
		updatingFFmpegEntry = true
		ffmpegTextEntry.SetText(path)
		updatingFFmpegEntry = false
		s.ffmpegPath, _ = utils.ResolveFFmpegPath("")
		s.markFFmpegPathChanged()
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

			p := filepath.ToSlash(lu.Path())
			ffmpegTextEntry.SetText(p)
		}, w)

		if f, ok := fd.(xfilepicker.FilePicker); ok {
			ffmpegDir := strings.TrimSpace(ffmpegTextEntry.Text)
			if ffmpegDir != "" {
				ffmpegURI := storage.NewFileURI(filepath.FromSlash(ffmpegDir))
				ffmpegLister, err := storage.ListerForURI(ffmpegURI)
				if err == nil {
					f.SetLocation(ffmpegLister)
				}
			}
		}

		resumeHotkeys = suspendHotkeys(s)
		fd.Show()
		fd.Resize(fyne.NewSize(filePickerFillSize, filePickerFillSize))
	})

	ffmpegRightButtons := container.NewHBox(ffmpegFolderSelect, ffmpegFolderReset)
	ffmpegPathControls := container.New(layout.NewBorderLayout(nil, nil, nil, ffmpegRightButtons), ffmpegRightButtons, ffmpegTextEntry)

	ffmpegTextEntry.Text = ffmpegDirDisplayPath(fyne.CurrentApp().Preferences().String("ffmpeg"))
	ffmpegTextEntry.Refresh()

	ffmpegTextEntry.OnChanged = func(update string) {
		if updatingFFmpegEntry {
			return
		}

		if strings.TrimSpace(update) == "" {
			s.ffmpegPath = ""
		} else if path, err := utils.ResolveFFmpegPath(update); err == nil {
			s.ffmpegPath = path
		} else {
			s.ffmpegPath = update
		}
		fyne.CurrentApp().Preferences().SetString("ffmpeg", update)
		s.markFFmpegPathChanged()
	}

	debugExport := widget.NewButton(lang.L("Export Diagnostics"), func() {
		showDiagnosticsSaveDialog(s)
	})

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
			target := traversalPlaybackTarget(s)
			switch selection {
			case "Enabled":
				switch s.getScreenState() {
				case "Playing", "Paused":
					if target.device.deviceType == devices.DeviceTypeDLNA {
						newTVPayload, err := queueNext(s, false)
						if err == nil && s.GaplessMediaWatcher == nil {
							s.GaplessMediaWatcher = gaplessMediaWatcher
							go s.GaplessMediaWatcher(s.serverStopCTX, s, newTVPayload)
						}
					}
				}
			case "Disabled":
				// We're disabling gapless playback. If for some reason
				// we fail to clear the NextURI it would be best to stop and
				// avoid inconsistencies where gapless playback appears disabled
				// but in reality it's not.
				if target.device.deviceType == devices.DeviceTypeDLNA && s.tvdata != nil {
					_, err := queueNext(s, true)
					if err != nil {
						stopAction(s)
					}
				}
			}
		}
	})
	gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
	gaplessdropdown.SetSelected(lang.L(gaplessOption))

	dropdownTheme.Refresh()

	sameTypeAutoNextCheck := widget.NewCheck(lang.L("Only Auto-Play Same File Types"), func(b bool) {
		s.setAutoPlaySameTypes(b)
	})

	sameTypeAutoNextOption := fyne.CurrentApp().Preferences().BoolWithFallback("AutoPlaySameTypes", true)
	sameTypeAutoNextCheck.SetChecked(sameTypeAutoNextOption)

	imageAutoSkipCurrent := widget.NewLabel("")
	imageAutoSkipSlider := widget.NewSlider(0, imageAutoSkipSecondsMax)
	imageAutoSkipSlider.Step = 1

	setImageAutoSkipCurrent := func(seconds int) {
		if seconds == 0 {
			imageAutoSkipCurrent.SetText(lang.L("Disabled"))
			return
		}

		imageAutoSkipCurrent.SetText(fmt.Sprintf("%ds", seconds))
	}

	initialImageAutoSkip := getImageAutoSkipSecondsPref()
	imageAutoSkipSlider.SetValue(float64(initialImageAutoSkip))
	setImageAutoSkipCurrent(initialImageAutoSkip)

	imageAutoSkipSlider.OnChanged = func(v float64) {
		rawSeconds := int(v + 0.5)
		seconds := clampImageAutoSkipSeconds(rawSeconds)
		if seconds != rawSeconds {
			imageAutoSkipSlider.SetValue(float64(seconds))
			return
		}
		fyne.CurrentApp().Preferences().SetInt(imageAutoSkipSecondsPref, seconds)
		setImageAutoSkipCurrent(seconds)
		s.refreshImageAutoSkipTimer()
	}

	imageAutoSkipControls := container.NewBorder(nil, nil, nil, imageAutoSkipCurrent, imageAutoSkipSlider)

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

	rtmpKeyContainer := container.NewBorder(nil, nil, nil, container.NewHBox(regenKeyBtn), streamKeyEntry)

	generalSettings := container.NewVBox(
		container.NewGridWithColumns(2,
			newSettingsField(lang.L("Theme"), dropdownTheme),
			newSettingsField(lang.L("Language"), dropdownLanguage),
		),
		newSettingsField("ffmpeg "+lang.L("Path"), ffmpegPathControls),
		newSettingsCheckboxField(rememberPlaybackPositionControls),
	)

	autoNextSettings := container.NewVBox(
		newSettingsField(lang.L("Gapless Playback"), gaplessdropdown),
		newSettingsCheckboxField(sameTypeAutoNextCheck),
		newSettingsField(lang.L("Image Auto-Skip Timeout"), imageAutoSkipControls),
	)

	rtmpSettings := container.NewVBox(
		newSettingsField(lang.L("RTMP Port"), rtmpPortEntry),
		newSettingsField(lang.L("RTMP Stream Key"), rtmpKeyContainer),
	)

	debugSettings := container.NewVBox(
		newSettingsField(lang.L("Diagnostics"), debugExport),
	)

	remoteSessionButton := widget.NewButton(lang.L("Remote Web Session")+"…", func() {
		s.openRemoteWebSessionDialog()
	})
	remoteSessionSettings := container.NewVBox(
		newSettingsField(lang.L("Remote Web Session"), remoteSessionButton),
	)

	leftColumn := container.NewVBox(
		widget.NewCard(lang.L("Common Options"), "", generalSettings),
		widget.NewCard(lang.L("Remote Web Session"), "", remoteSessionSettings),
		widget.NewCard(lang.L("Diagnostics"), "", debugSettings),
	)
	rightColumn := container.NewVBox(
		widget.NewCard(lang.L("Auto-Play Next File"), "", autoNextSettings),
		widget.NewCard(lang.L("RTMP Server"), "", rtmpSettings),
	)
	settingsCategories := container.NewGridWithColumns(2, leftColumn, rightColumn)

	return settingsCategories
}

func showDiagnosticsSaveDialog(s *FyneScreen) {
	if !diagnosticsAvailable(s) {
		fynedialog.ShowInformation(lang.L("Diagnostics"), lang.L("No diagnostics available"), s.Current)
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

		saveDiagnostics(writer, s)
	}, s.Current)

	if f, ok := fd.(interface{ SetFileName(string) }); ok {
		f.SetFileName(diagnosticsFileName())
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
}

func saveDiagnostics(f fyne.URIWriteCloser, s *FyneScreen) {
	w := s.Current
	defer f.Close()

	if err := writeDiagnostics(f, s); err != nil {
		fynedialog.ShowError(err, w)
		return
	}

	fynedialog.ShowInformation(lang.L("Diagnostics"), lang.L("Saved to")+"... "+f.URI().String(), w)
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
