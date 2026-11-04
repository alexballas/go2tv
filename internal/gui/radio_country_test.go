package gui

import (
	"testing"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

func TestRadioDisplayLanguageUsesPreferredLocales(t *testing.T) {
	tests := []struct {
		name    string
		locales []string
		want    language.Tag
	}{
		{name: "English preferred over Polish region", locales: []string{"en"}, want: language.English},
		{name: "English region", locales: []string{"en-US"}, want: language.English},
		{name: "Polish language", locales: []string{"pl"}, want: language.Polish},
		{name: "Polish region", locales: []string{"pl-PL"}, want: language.Polish},
		{name: "Polish before English", locales: []string{"pl-PL", "en-US"}, want: language.Polish},
		{name: "unsupported falls through to next preferred", locales: []string{"de-DE", "pl-PL"}, want: language.Polish},
		{name: "unsupported only falls back to English", locales: []string{"de-DE"}, want: language.English},
		{name: "empty falls back to English", want: language.English},
		{name: "simplified Chinese", locales: []string{"zh-Hans"}, want: language.SimplifiedChinese},
		{name: "traditional Chinese", locales: []string{"zh-Hant"}, want: language.TraditionalChinese},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := radioDisplayLanguageFor(tc.locales); got != tc.want {
				t.Fatalf("radioDisplayLanguageFor(%v) = %v, want %v", tc.locales, got, tc.want)
			}
		})
	}
}

func TestRadioCountryNamesUsePreferredLanguage(t *testing.T) {
	tests := []struct {
		name    string
		locales []string
		want    string
	}{
		{name: "English preferred", locales: []string{"en"}, want: "Germany"},
		{name: "Polish language", locales: []string{"pl"}, want: "Niemcy"},
		{name: "Polish region", locales: []string{"pl-PL"}, want: "Niemcy"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tag := radioDisplayLanguageFor(tc.locales)
			if got := radioRegionName(display.Regions(tag), "DE"); got != tc.want {
				t.Fatalf("country name for %v = %q, want %q", tc.locales, got, tc.want)
			}
		})
	}
}
