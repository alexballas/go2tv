package gui

import "sync/atomic"

func suspendHotkeys(screen *FyneScreen) func() {
	if screen == nil {
		return func() {}
	}

	atomic.AddInt32(&screen.hotkeysSuspendCount, 1)

	return func() {
		if screen == nil {
			return
		}
		if atomic.AddInt32(&screen.hotkeysSuspendCount, -1) < 0 {
			atomic.StoreInt32(&screen.hotkeysSuspendCount, 0)
		}
	}
}

func (screen *FyneScreen) hotkeysSuspended() bool {
	if screen == nil {
		return false
	}
	return atomic.LoadInt32(&screen.hotkeysSuspendCount) > 0
}
