package utils

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// ConvertSRTtoWebVTT converts an SRT subtitle file to WebVTT format.
// Returns the WebVTT content as bytes.
func ConvertSRTtoWebVTT(srtPath string) ([]byte, error) {
	file, err := os.Open(srtPath)
	if err != nil {
		return nil, fmt.Errorf("open srt: %w", err)
	}
	defer file.Close()

	return ConvertSRTReaderToWebVTT(file)
}

// ConvertSRTReaderToWebVTT converts SRT content from a reader to WebVTT.
func ConvertSRTReaderToWebVTT(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer

	// WebVTT header
	buf.WriteString("WEBVTT\n\n")

	scanner := bufio.NewScanner(r)
	// SRT uses comma for milliseconds, WebVTT uses dot
	timeRegex := regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

	for scanner.Scan() {
		line := scanner.Text()

		// Convert time format: 00:00:01,234 --> 00:00:04,567
		// To WebVTT format: 00:00:01.234 --> 00:00:04.567
		if strings.Contains(line, " --> ") {
			line = timeRegex.ReplaceAllString(line, "$1.$2")
		}

		buf.WriteString(line)
		buf.WriteString("\n")
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read srt: %w", err)
	}

	return buf.Bytes(), nil
}
