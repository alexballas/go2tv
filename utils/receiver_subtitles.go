package utils

import (
	"bytes"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	assOverrideBlock = regexp.MustCompile(`\{\\[^{}]*\}`)
	assAlignTag      = regexp.MustCompile(`(?i)\\an([1-9])`)
	assStyleTag      = regexp.MustCompile(`(?i)\\([biu])(\d*)`)
)

// assAlignmentToWebVTT maps ASS numpad alignment 1-9 to WebVTT cue settings.
// Returns "" when an is out of range.
// Bottom (1-3) omits line: so an2 matches untagged default bottom captions.
// Top (7-9) uses line:1 (device: line:0% flush to edge; line:1 matches default margin).
func assAlignmentToWebVTT(an int) string {
	switch an {
	case 1:
		return "align:left"
	case 2:
		return "align:center"
	case 3:
		return "align:right"
	case 4:
		return "line:50%,center align:left"
	case 5:
		return "line:50%,center align:center"
	case 6:
		return "line:50%,center align:right"
	case 7:
		return "line:1 align:left"
	case 8:
		return "line:1 align:center"
	case 9:
		return "line:1 align:right"
	}
	return ""
}

// cleanReceiverCueBody strips ASS override blocks, maps simple style tags to
// HTML, and normalizes ASS breaks. It returns the cleaned text and the last
// {\anN} alignment seen (0 if none).
func cleanReceiverCueBody(s string) (string, int) {
	// Guard \n/\h unescape on pre-strip body so paths survive when no overrides.
	hasOverride := strings.Contains(s, `{\`)
	// Drop drawing runs first while {\pN} markers still exist to toggle mode.
	s, _ = removeASSDrawingRuns(s)
	an := 0
	var open []string // open HTML tags: "b","i","u"
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		if s[0] == '{' {
			if end := strings.IndexByte(s, '}'); end >= 0 && strings.HasPrefix(s[1:], `\`) {
				block := s[:end+1]
				if m := assAlignTag.FindStringSubmatch(block); m != nil {
					if n, err := strconv.Atoi(m[1]); err == nil {
						an = n
					}
				}
				for _, m := range assStyleTag.FindAllStringSubmatch(block, -1) {
					tag := strings.ToLower(m[1])
					on := true
					switch m[2] {
					case "":
						// \b / \i / \u without arg = on
					case "0":
						on = false
					default:
						n, err := strconv.Atoi(m[2])
						on = err == nil && n != 0
					}
					if on {
						if !containsTag(open, tag) {
							b.WriteString("<" + tag + ">")
							open = append(open, tag)
						}
					} else {
						for i := len(open) - 1; i >= 0; i-- {
							if open[i] == tag {
								b.WriteString("</" + tag + ">")
								open = append(open[:i], open[i+1:]...)
								break
							}
						}
					}
				}
				s = s[end+1:]
				continue
			}
		}
		end := strings.IndexByte(s, '{')
		if end < 0 {
			end = len(s)
		}
		if end == 0 { // unclosed brace is literal
			end = 1
		}
		b.WriteString(s[:end])
		s = s[end:]
	}
	// Close still-open style tags at cue end.
	for _, tag := range slices.Backward(open) {
		b.WriteString("</" + tag + ">")
	}
	cleaned := b.String()
	cleaned = strings.ReplaceAll(cleaned, `\N`, "\n")
	if hasOverride {
		cleaned = strings.ReplaceAll(cleaned, `\n`, " ")
		cleaned = strings.ReplaceAll(cleaned, `\h`, " ")
	}
	cleaned = dropEmptyCueLines(cleaned)
	return cleaned, an
}

func containsTag(tags []string, tag string) bool {
	return slices.Contains(tags, tag)
}

// dropEmptyCueLines removes blank lines that would terminate a WebVTT/SRT cue.
func dropEmptyCueLines(s string) string {
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// applyWebVTTAlignment merges missing cue settings for an onto the timing line.
// Existing line:/align: are never overwritten; region: cues are left alone.
func applyWebVTTAlignment(timing string, an int) string {
	settings := assAlignmentToWebVTT(an)
	if settings == "" {
		return timing
	}
	if hasWebVTTSetting(timing, "region") {
		return timing
	}
	existing := webVTTSettingKeys(timing)
	var add []string
	for tok := range strings.FieldsSeq(settings) {
		key, _, _ := strings.Cut(tok, ":")
		if !existing[key] {
			add = append(add, tok)
		}
	}
	if len(add) == 0 {
		return timing
	}
	return timing + " " + strings.Join(add, " ")
}

func hasWebVTTSetting(timing, key string) bool {
	for tok := range strings.FieldsSeq(strings.ToLower(timing)) {
		k, _, ok := strings.Cut(tok, ":")
		if ok && k == key {
			return true
		}
	}
	return false
}

func webVTTSettingKeys(timing string) map[string]bool {
	keys := map[string]bool{}
	for tok := range strings.FieldsSeq(strings.ToLower(timing)) {
		if k, _, ok := strings.Cut(tok, ":"); ok {
			keys[k] = true
		}
	}
	return keys
}

// sanitizeReceiverWebVTT strips ASS overrides from cue bodies and merges
// alignment into WebVTT cue settings before seek retime.
func sanitizeReceiverWebVTT(data []byte) []byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	trailing := strings.HasSuffix(text, "\n")
	blocks := strings.Split(text, "\n\n")
	kept := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if isWebVTTNonCueBlock(block) {
			kept = append(kept, block)
			continue
		}
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) == 0 || lines[0] == "" {
			kept = append(kept, block)
			continue
		}
		timingIdx := 0
		if !strings.Contains(lines[0], "-->") && len(lines) > 1 {
			timingIdx = 1
		}
		if timingIdx >= len(lines) || !strings.Contains(lines[timingIdx], "-->") {
			kept = append(kept, block)
			continue
		}
		bodyStart := timingIdx + 1
		body := strings.Join(lines[bodyStart:], "\n")
		cleaned, bodyAn := cleanReceiverCueBody(body)
		if cleaned == "" {
			continue // drop empty cue
		}
		lines[timingIdx] = applyWebVTTAlignment(lines[timingIdx], bodyAn)
		lines = append(lines[:bodyStart], cleaned)
		kept = append(kept, strings.Join(lines, "\n"))
	}
	out := strings.Join(kept, "\n\n")
	if trailing && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out)
}

// stripReceiverSRTOverrides removes ASS override blocks from SRT cue bodies.
// SRT has no standard positioning, so alignment is discarded.
// Encoding-safe: UTF-16 is decoded before the override check; bytes without
// overrides are returned unchanged.
func stripReceiverSRTOverrides(data []byte) []byte {
	if isUTF16(data) {
		decoded, err := decodeASSCharset(data)
		if err != nil {
			return data
		}
		if !bytes.Contains(decoded, []byte(`{\`)) {
			return data
		}
		return stripReceiverSRTOverridesUTF8(decoded)
	}
	if !bytes.Contains(data, []byte(`{\`)) {
		return data
	}
	return stripReceiverSRTOverridesUTF8(data)
}

func isUTF16(data []byte) bool {
	if len(data) >= 2 && ((data[0] == 0xff && data[1] == 0xfe) || (data[0] == 0xfe && data[1] == 0xff)) {
		return true
	}
	return utf16WithoutBOM(data) != nil
}

func stripReceiverSRTOverridesUTF8(data []byte) []byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	trailing := strings.HasSuffix(text, "\n")
	blocks := strings.Split(text, "\n\n")
	kept := make([]string, 0, len(blocks))
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) == 0 || lines[0] == "" {
			kept = append(kept, block)
			continue
		}
		timingIdx := 0
		if !strings.Contains(lines[0], "-->") && len(lines) > 1 && looksLikeSRTIndex(lines[0]) {
			timingIdx = 1
		}
		if timingIdx >= len(lines) || !strings.Contains(lines[timingIdx], "-->") {
			kept = append(kept, block)
			continue
		}
		bodyStart := timingIdx + 1
		body := strings.Join(lines[bodyStart:], "\n")
		cleaned, _ := cleanReceiverCueBody(body)
		if cleaned == "" {
			continue
		}
		lines = append(lines[:bodyStart], cleaned)
		kept = append(kept, strings.Join(lines, "\n"))
	}
	out := strings.Join(kept, "\n\n")
	if trailing && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return []byte(out)
}

func looksLikeSRTIndex(s string) bool {
	s = strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isWebVTTNonCueBlock(block string) bool {
	return isWebVTTMetadataFirstLine(block)
}

// isWebVTTMetadataFirstLine reports whether the first line of a block is a
// WebVTT metadata keyword (exact word only — STYLE-1 is a cue id, not STYLE).
func isWebVTTMetadataFirstLine(block string) bool {
	fields := strings.Fields(strings.TrimSpace(block))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "WEBVTT", "NOTE", "STYLE", "REGION":
		return true
	}
	return false
}
