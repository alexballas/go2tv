package gui

import "testing"

func TestHotkeysSuspend(t *testing.T) {
	t.Run("nil_screen_no_panic", func(t *testing.T) {
		resume := suspendHotkeys(nil)
		resume()
	})

	t.Run("single_suspend", func(t *testing.T) {
		s := &FyneScreen{Hotkeys: true}

		if s.hotkeysSuspended() {
			t.Fatalf("expected not suspended")
		}

		resume := suspendHotkeys(s)
		if !s.hotkeysSuspended() {
			t.Fatalf("expected suspended")
		}

		resume()
		if s.hotkeysSuspended() {
			t.Fatalf("expected not suspended after resume")
		}
	})

	t.Run("nested_suspend", func(t *testing.T) {
		s := &FyneScreen{Hotkeys: true}

		resume1 := suspendHotkeys(s)
		resume2 := suspendHotkeys(s)

		if !s.hotkeysSuspended() {
			t.Fatalf("expected suspended")
		}

		resume1()
		if !s.hotkeysSuspended() {
			t.Fatalf("expected still suspended after one resume")
		}

		resume2()
		if s.hotkeysSuspended() {
			t.Fatalf("expected not suspended after both resumes")
		}
	})

	t.Run("resume_more_than_once", func(t *testing.T) {
		s := &FyneScreen{Hotkeys: true}
		resume := suspendHotkeys(s)

		resume()
		resume()

		if s.hotkeysSuspended() {
			t.Fatalf("expected not suspended")
		}
	})
}
