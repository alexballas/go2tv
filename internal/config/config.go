package config

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

type Config struct {
	Theme string `json:"theme"`
}

type go2tvTheme struct {
	Theme string
}

func (m go2tvTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch m.Theme {
	case "Dark":
		variant = theme.VariantDark
	case "Light":
		variant = theme.VariantLight
	}

	return theme.DefaultTheme().Color(name, variant)
}

func (m go2tvTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (m go2tvTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (m go2tvTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

func GetAppConfig() (*Config, error) {
	path, err := appPath()
	if err != nil {
		return nil, fmt.Errorf("GetAppConfig: failed to access config path due to error %w:", err)
	}

	cfgfile, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			err := os.MkdirAll(filepath.Dir(path), 0700)
			if err != nil {
				return nil, fmt.Errorf("GetAppConfig: failed to create default path due to error %w:", err)
			}

			// Set default config here
			conf := &Config{
				Theme: "Default",
			}

			b, err := json.Marshal(conf)
			if err != nil {
				return nil, fmt.Errorf("GetAppConfig: failed to convert and store default config %w:", err)
			}

			if err := os.WriteFile(path, b, 0644); err != nil {
				return nil, fmt.Errorf("GetAppConfig: failed to create default config due to error %w:", err)
			}

			return conf, nil
		}

		return nil, fmt.Errorf("GetAppConfig: failed to open config due to error %w:", err)
	}
	defer cfgfile.Close()

	conf := &Config{}
	if err := json.NewDecoder(cfgfile).Decode(conf); err != nil {
		return nil, fmt.Errorf("GetAppConfig: failed to decode config due to error %w:", err)
	}

	return conf, nil
}

func appPath() (string, error) {
	oscfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("appPath: failed to get config file due to error %w:", err)
	}

	return fmt.Sprint(filepath.Join(oscfg, "go2tv", "settings.json")), nil
}

func (s *Config) ApplyAppConfig() {
	switch s.Theme {
	case "Dark":
		fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Dark"})
	case "Light":
		fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Light"})
	case "Default":
		fyne.CurrentApp().Settings().SetTheme(theme.DefaultTheme())
	}
}

func (s *Config) SaveAppConfig() error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("SaveAppConfig: failed to marshal json due to error %w:", err)
	}

	path, err := appPath()
	if err != nil {
		return fmt.Errorf("SaveAppConfig: failed to access config path due to error %w:", err)
	}

	if err := os.WriteFile(path, b, 0655); err != nil {
		return fmt.Errorf("SaveAppConfig: failed save config due to error %w:", err)
	}

	return nil
}
