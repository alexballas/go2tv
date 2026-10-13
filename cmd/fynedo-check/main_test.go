package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDirectoryResolvesMethodReceivers(t *testing.T) {
	const definitions = `package gui
import fyne "github.com/alexballas/refyne/v2"
type label struct{}
func (*label) SetText(string) {}
type view struct { label *label }
func (v *view) refresh() { v.label.SetText("unsafe") }
func (v *view) update() { v.refresh() }
type bridge struct { label *label }
func (b *bridge) refresh() { fyne.Do(func() { b.label.SetText("safe") }) }
func newBridge() *bridge { return &bridge{} }
`
	tt := []struct {
		name   string
		source string
		want   int
	}{
		{name: "safe method shares unsafe method name", source: `func run(b *bridge) { go b.refresh() }`},
		{name: "unsafe receiver still reported", source: `func run(v *view) { go v.refresh() }`, want: 1},
		{name: "unsafe helper chain still reported", source: `func run(v *view) { go v.update() }`, want: 1},
		{name: "constructor inferred receiver", source: `func run() { b := newBridge(); go b.refresh() }`},
		{name: "composite literal inferred receiver", source: `func run() { v := &view{}; go v.refresh() }`, want: 1},
		{name: "shadowed receiver", source: `func run(b *view) { { b := &bridge{}; go b.refresh() } }`},
		{name: "receiver alias", source: `type alias = view; func run(v *alias) { go v.refresh() }`, want: 1},
		{name: "UI dispatch remains safe", source: `func run(v *view) { go func() { fyne.DoAndWait(func() { v.refresh() }) }() }`},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeCheckerFixture(t, dir, "types.go", definitions)
			writeCheckerFixture(t, dir, "caller.go", "package gui\nimport fyne \"github.com/alexballas/refyne/v2\"\n"+tc.source)
			violations, err := checkDirectory(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != tc.want {
				t.Fatalf("violations = %+v, want %d", violations, tc.want)
			}
			if tc.want > 0 && (filepath.Base(violations[0].File) != "types.go" || violations[0].Widget != "v.label" || violations[0].Method != "SetText()") {
				t.Fatalf("unsafe widget mutation not identified: %+v", violations)
			}
		})
	}
}

func TestCheckDirectoryDoesNotCrossPackageBoundaries(t *testing.T) {
	dir := t.TempDir()
	writeCheckerFixture(t, dir, "gui.go", `package gui
type label struct{}
func (*label) SetText(string) {}
func refresh(l *label) { l.SetText("unsafe") }
`)
	writeCheckerFixture(t, dir, "external_test.go", `package gui_test
func refresh() {}
func run() { go refresh() }
`)
	subdir := filepath.Join(dir, "other")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	writeCheckerFixture(t, subdir, "other.go", `package gui
func refresh() {}
func run() { go refresh() }
`)
	violations, err := checkDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("unrelated package function was followed: %+v", violations)
	}
}

func TestCheckDirectoryChecksPlatformHelpersIndependently(t *testing.T) {
	tt := []struct {
		name                        string
		unsafeDesktop, unsafeMobile bool
		want                        int
	}{
		{name: "unsafe mobile", unsafeMobile: true, want: 1},
		{name: "unsafe desktop", unsafeDesktop: true, want: 1},
		{name: "both unsafe", unsafeDesktop: true, unsafeMobile: true, want: 2},
		{name: "both safe"},
	}
	for _, kind := range []string{"function", "method"} {
		for _, tc := range tt {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				common := "package gui\ntype label struct{}\nfunc (*label) SetText(string) {}\n"
				entry := "func run(l *label) { go refresh(l) }\n"
				declaration := "func refresh(l *label) { BODY }\n"
				if kind == "method" {
					entry = "func run(b *bridge) { go b.refresh() }\n"
					declaration = "type bridge struct { label *label }; func (b *bridge) refresh() { l := b.label; BODY }\n"
				}
				writeCheckerFixture(t, dir, "common.go", common+entry)
				variants := []struct {
					file, constraint string
					unsafe           bool
				}{
					{"a_desktop.go", "!(android || ios)", tc.unsafeDesktop},
					{"z_mobile.go", "android || ios", tc.unsafeMobile},
				}
				for _, variant := range variants {
					body := `fyne.Do(func() { l.SetText("safe") })`
					if variant.unsafe {
						body = `l.SetText("unsafe")`
					}
					source := "//go:build " + variant.constraint + "\n\npackage gui\nimport fyne \"github.com/alexballas/refyne/v2\"\n" + strings.ReplaceAll(declaration, "BODY", body)
					writeCheckerFixture(t, dir, variant.file, source)
				}
				violations, err := checkDirectory(dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(violations) != tc.want {
					t.Fatalf("platform violations = %+v, want %d", violations, tc.want)
				}
				for _, violation := range violations {
					file := filepath.Base(violation.File)
					if violation.Method != "SetText()" || (file == "a_desktop.go" && !tc.unsafeDesktop) || (file == "z_mobile.go" && !tc.unsafeMobile) || (file != "a_desktop.go" && file != "z_mobile.go") {
						t.Fatalf("wrong platform helper reported: %+v", violation)
					}
				}
			})
		}
	}
}

func TestCheckDirectoryRejectsUnreadableOrInvalidSource(t *testing.T) {
	tt := []struct {
		name    string
		missing bool
	}{
		{name: "missing directory", missing: true},
		{name: "invalid Go source"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.missing {
				dir = filepath.Join(dir, "missing")
			} else {
				writeCheckerFixture(t, dir, "broken.go", "package gui\nfunc broken(")
			}
			if _, err := checkDirectory(dir); err == nil {
				t.Fatal("checker silently accepted unreadable or invalid source")
			}
		})
	}
}

func writeCheckerFixture(t *testing.T, dir, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}
