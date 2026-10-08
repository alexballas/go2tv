//go:build !linux && !(android || ios)

package gui

func startMPRIS(*FyneScreen) mprisBridge { return nil }
