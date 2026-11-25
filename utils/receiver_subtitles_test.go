package utils

import (
	"strings"
	"testing"
)

func TestCleanReceiverCueBody(t *testing.T) {
	tt := []struct {
		name, in, want string
		wantAn         int
	}{
		{name: "strip an8", in: `{\an8}Top line`, want: "Top line", wantAn: 8},
		{name: "b tag to html", in: `{\an2\pos(10,20)\b1}Bottom{\b0}`, want: "<b>Bottom</b>", wantAn: 2},
		{name: "keep html tags", in: `<i>Italic</i> and <b>bold</b> and <font color="#f00">red</font>`, want: `<i>Italic</i> and <b>bold</b> and <font color="#f00">red</font>`, wantAn: 0},
		{name: "italic from ass strip", in: `{\i1}Italic{\i0} normal`, want: "<i>Italic</i> normal", wantAn: 0},
		{name: "unclosed italic closed at end", in: `{\i1}Italic`, want: "<i>Italic</i>", wantAn: 0},
		{name: "hard breaks with override", in: `{\an8}Line1\NLine2\hX`, want: "Line1\nLine2 X", wantAn: 8},
		{name: "windows path no override untouched", in: `C:\home\notes`, want: `C:\home\notes`, wantAn: 0},
		{name: "Network path no override untouched", in: `C:\Network`, want: `C:\Network`, wantAn: 0},
		{name: "soft break becomes space with override", in: `{\b1}a\nb{\b0}`, want: "<b>a b</b>", wantAn: 0},
		{name: "unclosed brace is literal", in: `oops {an8 still`, want: "oops {an8 still", wantAn: 0},
		{name: "last an wins", in: `{\an7}a{\an9}b`, want: "ab", wantAn: 9},
		{name: "pos and move stripped", in: `{\pos(1,2)\move(3,4,5,6)}text`, want: "text", wantAn: 0},
		{name: "drawing run dropped", in: `{\p1}m 0 0 l 100 0{\p0}Hello`, want: "Hello", wantAn: 0},
		{name: "empty after clean", in: `{\pos(1,2)}`, want: "", wantAn: 0},
		{name: "blank lines dropped with override", in: `{\an8}A\N\NB`, want: "A\nB", wantAn: 8},
		// Style-token whole match (no prefix false positives)
		{name: "bord not bold", in: `{\bord2}x`, want: "x", wantAn: 0},
		{name: "blur not bold", in: `{\blur3}x`, want: "x", wantAn: 0},
		{name: "be not bold", in: `{\be1}x`, want: "x", wantAn: 0},
		{name: "BORD case not bold", in: `{\BORD2}x`, want: "x", wantAn: 0},
		{name: "iclip not italic", in: `{\iclip(0,0,1,1)}x`, want: "x", wantAn: 0},
		{name: "transform b1 ignored", in: `{\t(\b1)}x`, want: "x", wantAn: 0},
		{name: "transform with nested pos", in: `{\t(\pos(1,2))}x`, want: "x", wantAn: 0},
		{name: "unclosed transform skip", in: `{\t(\b1`, want: `{\t(\b1`, wantAn: 0},
		{name: "bare b is off", in: `{\b}x`, want: "x", wantAn: 0},
		{name: "b400 not bold", in: `{\b400}x`, want: "x", wantAn: 0},
		{name: "b700 is bold", in: `{\b700}x`, want: "<b>x</b>", wantAn: 0},
		{name: "nested close properly", in: `{\i1}a{\b1}b{\i0}c`, want: "<i>a<b>b</b></i>c", wantAn: 0},
		{name: "reset closes stack", in: `{\i1}a{\r}b`, want: "<i>a</i>b", wantAn: 0},
		{name: "i1 b1 in one block", in: `{\i1\b1}x`, want: "<i><b>x</b></i>", wantAn: 0},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got, an := cleanReceiverCueBody(tc.in)
			if got != tc.want || an != tc.wantAn {
				t.Fatalf("got (%q, %d), want (%q, %d)", got, an, tc.want, tc.wantAn)
			}
		})
	}
}

func TestApplyWebVTTAlignment(t *testing.T) {
	tt := []struct {
		name, timing string
		an           int
		want         string
	}{
		{name: "an8 top", timing: "00:01.000 --> 00:04.000", an: 8, want: "00:01.000 --> 00:04.000 line:1 align:center"},
		{name: "an2 bottom only align", timing: "00:01.000 --> 00:04.000", an: 2, want: "00:01.000 --> 00:04.000 align:center"},
		{name: "an1 bottom left", timing: "00:01.000 --> 00:04.000", an: 1, want: "00:01.000 --> 00:04.000 align:left"},
		{name: "an5 mid", timing: "00:01.000 --> 00:04.000", an: 5, want: "00:01.000 --> 00:04.000 line:50%,center align:center"},
		{name: "merge line onto existing align", timing: "00:01.000 --> 00:04.000 align:start", an: 8, want: "00:01.000 --> 00:04.000 align:start line:1"},
		{name: "keep existing line add align", timing: "00:01.000 --> 00:04.000 line:50%", an: 8, want: "00:01.000 --> 00:04.000 line:50% align:center"},
		{name: "region untouched", timing: "00:01.000 --> 00:04.000 region:foo", an: 8, want: "00:01.000 --> 00:04.000 region:foo"},
		{name: "no an", timing: "00:01.000 --> 00:04.000", an: 0, want: "00:01.000 --> 00:04.000"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyWebVTTAlignment(tc.timing, tc.an); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeReceiverWebVTT(t *testing.T) {
	t.Run("strip an8 and inject settings", func(t *testing.T) {
		in := "WEBVTT\n\n00:01.000 --> 00:04.000\n{\\an8}Top line\n"
		got := string(sanitizeReceiverWebVTT([]byte(in)))
		if strings.Contains(got, `{\`) {
			t.Fatalf("override leaked: %q", got)
		}
		if !strings.Contains(got, "line:1 align:center") {
			t.Fatalf("settings missing: %q", got)
		}
		if !strings.Contains(got, "Top line") {
			t.Fatalf("text missing: %q", got)
		}
	})
	t.Run("plain vtt stays identical", func(t *testing.T) {
		in := "WEBVTT\n\n00:01.000 --> 00:02.000\nHello\n"
		got := string(sanitizeReceiverWebVTT([]byte(in)))
		if got != in {
			t.Fatalf("got %q want %q", got, in)
		}
	})
	t.Run("style css braces kept", func(t *testing.T) {
		in := "WEBVTT\n\nSTYLE\n::cue { color: lime; }\n\n00:01.000 --> 00:02.000 align:start\nHello\n"
		got := string(sanitizeReceiverWebVTT([]byte(in)))
		if !strings.Contains(got, "::cue { color: lime; }") {
			t.Fatalf("style damaged: %q", got)
		}
		if !strings.Contains(got, "align:start") {
			t.Fatalf("align lost: %q", got)
		}
	})
	t.Run("cue id STYLE-1 is a cue", func(t *testing.T) {
		in := "WEBVTT\n\nSTYLE-1\n00:01.000 --> 00:04.000\n{\\an8}Tagged\n"
		got := string(sanitizeReceiverWebVTT([]byte(in)))
		if !strings.Contains(got, "line:1 align:center") {
			t.Fatalf("STYLE-1 cue not sanitized: %q", got)
		}
	})
	t.Run("empty cue dropped", func(t *testing.T) {
		in := "WEBVTT\n\n00:01.000 --> 00:04.000\n{\\pos(1,2)}\n"
		got := string(sanitizeReceiverWebVTT([]byte(in)))
		if strings.Contains(got, "00:01.000 --> 00:04.000") {
			t.Fatalf("empty cue kept: %q", got)
		}
	})
	t.Run("idempotent", func(t *testing.T) {
		in := "WEBVTT\n\n00:01.000 --> 00:04.000\n{\\an8}Top\n"
		once := sanitizeReceiverWebVTT([]byte(in))
		twice := sanitizeReceiverWebVTT(once)
		if string(once) != string(twice) {
			t.Fatalf("not idempotent: %q vs %q", once, twice)
		}
	})
}

func TestStripReceiverSRTOverrides(t *testing.T) {
	t.Run("utf8 with tags", func(t *testing.T) {
		in := "1\n00:00:01,000 --> 00:00:04,000\n{\\an8}<i>Italic</i> top\n"
		got := string(stripReceiverSRTOverrides([]byte(in)))
		if strings.Contains(got, `{\`) {
			t.Fatalf("override leaked: %q", got)
		}
		if !strings.Contains(got, "<i>Italic</i> top") {
			t.Fatalf("html/text lost: %q", got)
		}
		if strings.Contains(got, "align:") || strings.Contains(got, "line:") {
			t.Fatalf("vtt settings on srt: %q", got)
		}
	})
	t.Run("utf8 without tags byte-identical", func(t *testing.T) {
		in := []byte("1\r\n00:00:01,000 --> 00:00:04,000\r\nHello\r\n")
		got := stripReceiverSRTOverrides(in)
		if string(got) != string(in) {
			t.Fatalf("got %q want %q", got, in)
		}
	})
	t.Run("utf16 without tags byte-identical", func(t *testing.T) {
		// UTF-16 LE BOM + "1\n"
		in := []byte{0xff, 0xfe, 0x31, 0x00, 0x0a, 0x00}
		got := stripReceiverSRTOverrides(in)
		if string(got) != string(in) {
			t.Fatalf("utf16 no-tag mutated: %v", got)
		}
	})
	t.Run("bom index first cue stripped", func(t *testing.T) {
		in := "\ufeff1\n00:00:01,000 --> 00:00:04,000\n{\\an8}Top\n"
		got := string(stripReceiverSRTOverrides([]byte(in)))
		if strings.Contains(got, `{\`) {
			t.Fatalf("bom cue not stripped: %q", got)
		}
	})
}

func TestSplitSubtitleBlocks(t *testing.T) {
	in := "1\n00:00:01,000 --> 00:00:04,000\n{\\an8}A\n \n2\n00:00:05,000 --> 00:00:08,000\nB\n"
	blocks := splitSubtitleBlocks(strings.ReplaceAll(in, "\r\n", "\n"))
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2: %q", len(blocks), blocks)
	}
	got := string(stripReceiverSRTOverrides([]byte(in)))
	// two cues should remain
	if strings.Count(got, "-->") != 2 {
		t.Fatalf("cues after strip = %q", got)
	}
}
