//go:build !(android || ios)

package servermode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
)

type logLevel string

const (
	levelDebug   logLevel = "DEBUG"
	levelInfo    logLevel = "INFO"
	levelWarning logLevel = "WARNING"
	levelError   logLevel = "ERROR"
)

type serverLogger struct {
	mu     sync.Mutex
	output io.Writer
	debug  bool
	color  bool
	now    func() time.Time
}

func newServerLogger(output io.Writer, debug bool) *serverLogger {
	if output == nil {
		output = io.Discard
	}
	styledOutput, color := terminalOutput(output)
	return &serverLogger{output: styledOutput, debug: debug, color: color, now: time.Now}
}

func terminalOutput(output io.Writer) (io.Writer, bool) {
	file, ok := output.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" || os.Getenv("CLICOLOR") == "0" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return output, false
	}
	fd := file.Fd()
	if !isatty.IsTerminal(fd) && !isatty.IsCygwinTerminal(fd) {
		return output, false
	}
	return colorable.NewColorable(file), true
}

func (l *serverLogger) Debug(message string)   { l.log(levelDebug, message) }
func (l *serverLogger) Info(message string)    { l.log(levelInfo, message) }
func (l *serverLogger) Warning(message string) { l.log(levelWarning, message) }
func (l *serverLogger) Error(message string)   { l.log(levelError, message) }

func (l *serverLogger) log(level logLevel, message string) {
	if level == levelDebug && !l.debug {
		return
	}
	l.write(level, l.now(), message)
}

func (l *serverLogger) write(level logLevel, timestamp time.Time, message string) {
	label, gap := string(level), " "
	if level == levelInfo {
		gap = "  "
	}
	if l.color {
		label = levelColor(level) + label + "\x1b[0m"
	}
	message = singleLine(message)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.output, "[%s] %s%s%s\n", timestamp.Format("2006-01-02 15:04:05"), label, gap, message)
}

func levelColor(level logLevel) string {
	switch level {
	case levelDebug:
		return "\x1b[90m"
	case levelInfo:
		return "\x1b[36m"
	case levelWarning:
		return "\x1b[33m"
	case levelError:
		return "\x1b[31m"
	default:
		return ""
	}
}

func singleLine(message string) string {
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "\n", " ")
	message = strings.ReplaceAll(message, "\t", " ")
	return strings.TrimSpace(message)
}

func (l *serverLogger) protocolOutput() io.Writer {
	return &protocolLogWriter{logger: l}
}

type protocolLogWriter struct {
	mu     sync.Mutex
	buffer []byte
	logger *serverLogger
}

func (w *protocolLogWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, data...)
	for {
		newline := bytes.IndexByte(w.buffer, '\n')
		if newline < 0 {
			break
		}
		line := append([]byte(nil), w.buffer[:newline]...)
		w.buffer = w.buffer[newline+1:]
		w.writeLine(line)
	}
	return len(data), nil
}

func (w *protocolLogWriter) writeLine(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var record map[string]any
	if json.Unmarshal(line, &record) != nil {
		w.logger.Debug("Protocol: " + string(line))
		return
	}
	protocolLevel := parseLevel(record["level"])
	// Protocol libraries log individual attempts before their retry and fallback
	// logic finishes. Only the controller knows whether an operation truly failed.
	// Keep every attempt-level record in DEBUG, including WARN/ERROR records.
	if !w.logger.debug {
		return
	}
	timestamp := w.logger.now()
	if value, ok := record["time"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			timestamp = parsed
		}
	}
	message, _ := record["msg"].(string)
	delete(record, "time")
	delete(record, "level")
	delete(record, "msg")
	if protocolLevel != levelDebug {
		record["protocol_level"] = string(protocolLevel)
	}
	w.logger.write(levelDebug, timestamp, protocolMessage(message, record))
}

func parseLevel(value any) logLevel {
	switch strings.ToUpper(fmt.Sprint(value)) {
	case "INFO":
		return levelInfo
	case "WARN", "WARNING":
		return levelWarning
	case "ERROR":
		return levelError
	default:
		return levelDebug
	}
}

func protocolMessage(message string, attrs map[string]any) string {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, logValue(attrs[key])))
	}
	result := "Protocol"
	if message != "" {
		result += ": " + message
	}
	if len(parts) != 0 {
		result += " (" + strings.Join(parts, ", ") + ")"
	}
	return result
}

func logValue(value any) string {
	if text, ok := value.(string); ok {
		return singleLine(text)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
