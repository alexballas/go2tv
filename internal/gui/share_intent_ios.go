//go:build ios

package gui

// registerShareHandler does nothing on iOS. Share targets there need a separate
// extension target and security-scoped URLs with different lifetime rules, none of
// which exists yet - but Start is shared by both mobile platforms, so the symbol
// has to.
func registerShareHandler(*FyneScreen) {}
