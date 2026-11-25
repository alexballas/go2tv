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
	assStyleToken    = regexp.MustCompile(`^([biu])(\d*)$`)
	// webVTTMarkup matches what WebVTT cue text may keep unescaped: tags
	// (incl. timestamps) and character references.
	webVTTMarkup = regexp.MustCompile(`^(?:</?[A-Za-z0-9][^<>]*>|&(?:[A-Za-z]+|#[0-9]+|#[xX][0-9A-Fa-f]+);)`)
)

// assAlignmentToWebVTT maps ASS numpad alignment 1-9 to WebVTT cue settings.
// Returns "" when an is out of range. Tuned on a Chromecast:
//   - Bottom (1-3) omits line: so an2 matches untagged default bottom captions.
//   - Middle (4-6) uses line:6. line:50%,center is ignored, and percentage
//     lines break position:/size:.
//   - Top (7-9) uses line:1 (line:0% is flush; line:1 matches bottom margin).
//   - Left/right get a 5% side margin; full-width boxes touch the edge.
func assAlignmentToWebVTT(an int) string {
	const (
		left  = "position:5% size:90% align:left"
		right = "position:95% size:90% align:right"
	)
	switch an {
	case 1:
		return left
	case 2:
		return "align:center"
	case 3:
		return right
	case 4:
		return "line:6 " + left
	case 5:
		return "line:6 align:center"
	case 6:
		return "line:6 " + right
	case 7:
		return "line:1 " + left
	case 8:
		return "line:1 align:center"
	case 9:
		return "line:1 " + right
	}
	return ""
}

// cleanReceiverCueBody strips ASS override blocks, maps simple style tags to
// HTML, and normalizes ASS breaks. It returns the cleaned text and the last
// {\anN} alignment seen (0 if none).
func cleanReceiverCueBody(s string) (string, int) {
	// Guard all ASS unescape on pre-strip body so paths survive without overrides.
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
				open = applyOverrideStyleTokens(block, &b, open)
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
	if hasOverride {
		cleaned = strings.ReplaceAll(cleaned, `\N`, "\n")
		cleaned = strings.ReplaceAll(cleaned, `\n`, " ")
		cleaned = strings.ReplaceAll(cleaned, `\h`, " ")
	}
	cleaned = dropEmptyCueLines(cleaned)
	return cleaned, an
}

// applyOverrideStyleTokens walks one {\...} block with a linear scanner.
// Whole-token match only (\bord/\blur/\iclip must not become b/i).
// \t(...) is skipped with paren-depth counting. Returns the updated open stack.
func applyOverrideStyleTokens(block string, b *strings.Builder, open []string) []string {
	// Interior without braces: starts with '\'
	body := block[1 : len(block)-1]
	i := 0
	for i < len(body) {
		if body[i] != '\\' {
			i++
			continue
		}
		// \t( ... ) — skip entire transform, count nested parens
		if strings.HasPrefix(body[i:], `\t(`) {
			depth := 0
			j := i + 2 // at '('
			for j < len(body) {
				switch body[j] {
				case '(':
					depth++
				case ')':
					depth--
					if depth == 0 {
						j++
						i = j
						goto next
					}
				}
				j++
			}
			// unclosed \t( — skip to end of block
			return open
		next:
			continue
		}
		// Token until next '\' or end
		j := i + 1
		for j < len(body) && body[j] != '\\' {
			j++
		}
		token := strings.TrimSpace(body[i+1 : j])
		open = applyStyleToken(token, b, open)
		i = j
	}
	return open
}

func applyStyleToken(token string, b *strings.Builder, open []string) []string {
	// \r and \r<style> reset to a style; no other ASS tag starts with r.
	if strings.HasPrefix(token, "r") {
		return closeAllTags(b, open)
	}
	m := assStyleToken.FindStringSubmatch(token)
	if m == nil {
		return open // \an, \pos, \bord, \blur, \be, \iclip, \BORD, …
	}
	tag := m[1] // case-sensitive b|i|u only
	var on bool
	switch tag {
	case "b":
		if m[2] == "" || m[2] == "0" {
			on = false // bare \b and \b0 reset to normal
		} else {
			n, err := strconv.Atoi(m[2])
			// \b1 = bold; weights < 700 are normal weight
			on = err == nil && (n == 1 || n >= 700)
		}
	default: // i, u
		on = m[2] == "1"
	}
	if on {
		return openTag(b, open, tag)
	}
	return closeTag(b, open, tag)
}

func openTag(b *strings.Builder, open []string, tag string) []string {
	if slices.Contains(open, tag) {
		return open
	}
	b.WriteString("<" + tag + ">")
	return append(open, tag)
}

// closeTag closes tag and everything opened after it (innermost first), then
// reopens the later tags so they stay nested and keep applying.
func closeTag(b *strings.Builder, open []string, tag string) []string {
	idx := slices.Index(open, tag)
	if idx < 0 {
		return open
	}
	for _, t := range slices.Backward(open[idx:]) {
		b.WriteString("</" + t + ">")
	}
	reopen := slices.Clone(open[idx+1:])
	open = open[:idx]
	for _, t := range reopen {
		b.WriteString("<" + t + ">")
		open = append(open, t)
	}
	return open
}

func closeAllTags(b *strings.Builder, open []string) []string {
	for _, t := range slices.Backward(open) {
		b.WriteString("</" + t + ">")
	}
	return nil
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

// splitSRTBlocks splits SRT on empty or whitespace-only lines; some SRT
// writers put spaces on separator lines.
func splitSRTBlocks(text string) []string {
	return splitSubtitleBlocks(text, func(line string) bool { return strings.TrimSpace(line) == "" })
}

// splitWebVTTBlocks splits WebVTT on empty lines only; per spec a
// whitespace-only line is cue text, not a separator.
func splitWebVTTBlocks(text string) []string {
	return splitSubtitleBlocks(text, func(line string) bool { return line == "" })
}

func splitSubtitleBlocks(text string, isSeparator func(string) bool) []string {
	lines := strings.Split(text, "\n")
	var blocks []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range lines {
		if isSeparator(line) {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return blocks
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
	// position: and size: form one box; never mix ours with the cue's own.
	ownBox := existing["position"] || existing["size"]
	var add []string
	for tok := range strings.FieldsSeq(settings) {
		key, _, _ := strings.Cut(tok, ":")
		if !existing[key] && !(ownBox && (key == "position" || key == "size")) {
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
		k, _, _ := strings.Cut(tok, ":")
		if k == key {
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
	blocks := splitWebVTTBlocks(text)
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
		cleaned = escapeWebVTTCueText(cleaned)
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

// escapeWebVTTCueText escapes stray < and & (receivers swallow "a < b") and
// --> (ends the cue) while keeping tags and character references.
func escapeWebVTTCueText(s string) string {
	if !strings.ContainsAny(s, "<&") && !strings.Contains(s, "-->") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		switch {
		case s[i] == '<' || s[i] == '&':
			if m := webVTTMarkup.FindString(s[i:]); m != "" {
				b.WriteString(m)
				i += len(m)
				continue
			}
			if s[i] == '<' {
				b.WriteString("&lt;")
			} else {
				b.WriteString("&amp;")
			}
		case strings.HasPrefix(s[i:], "-->"):
			b.WriteString("--&gt;")
			i += 3
			continue
		default:
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String()
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
	blocks := splitSRTBlocks(text)
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
