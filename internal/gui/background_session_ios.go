//go:build ios

package gui

// Keeping a cast alive while the app is backgrounded is an Android problem with
// an Android answer. Desktop builds are never frozen, and iOS has no background
// mode that covers holding a network session open - its audio mode is for apps
// producing audio through their own audio session, which go2tv does not do, so
// there is no partial version of this to build there.

func prepareBackgroundSession(screen *FyneScreen) {}

func beginBackgroundSession(screen *FyneScreen) {}

func syncBackgroundSession(screen *FyneScreen, state string) {}
