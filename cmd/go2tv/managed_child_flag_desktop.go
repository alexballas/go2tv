//go:build !(android || ios)

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// The internal -managed-child flag exists only on desktop go2tv: the GUI
// spawns its own binary with it to run a GUI-managed -server child. It is
// hidden from usage output; go2tv-lite and mobile never register it, so they
// reject it at flag parse.
func init() {
	flag.CommandLine.BoolVar(&serverOptions.ManagedChild, "managed-child", false, "Internal: run as a GUI-managed server child (requires -server).")
	installUsageHidingFlags(flag.CommandLine, "managed-child")
}

// installUsageHidingFlags replaces Usage with a PrintDefaults reimplementation
// that skips hidden flag names. The stdlib flag package has no hidden-flag
// support, so this is the only way to keep -managed-child out of -help while
// keeping every public flag listed.
func installUsageHidingFlags(flags *flag.FlagSet, hidden ...string) {
	hiddenNames := make(map[string]struct{}, len(hidden))
	for _, name := range hidden {
		hiddenNames[name] = struct{}{}
	}
	flags.Usage = func() {
		out := flags.Output()
		fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
		flags.VisitAll(func(f *flag.Flag) {
			if _, ok := hiddenNames[f.Name]; ok {
				return
			}
			var b strings.Builder
			fmt.Fprintf(&b, "  -%s", f.Name)
			name, usage := flag.UnquoteUsage(f)
			if len(name) > 0 {
				b.WriteString(" ")
				b.WriteString(name)
			}
			if b.Len() <= 4 {
				b.WriteString("\t")
			} else {
				b.WriteString("\n    \t")
			}
			b.WriteString(strings.ReplaceAll(usage, "\n", "\n    \t"))
			if f.DefValue != "" && f.DefValue != "false" {
				if name == "string" {
					fmt.Fprintf(&b, " (default %q)", f.DefValue)
				} else {
					fmt.Fprintf(&b, " (default %v)", f.DefValue)
				}
			}
			fmt.Fprint(out, b.String(), "\n")
		})
	}
}
