package platform

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"
	"sync"
	"time"
)

type logLevel string

const (
	logLevelDebug logLevel = "DEBUG"
	logLevelInfo  logLevel = "INFO"
	logLevelWarn  logLevel = "WARN"
	logLevelError logLevel = "ERROR"
)

type appLogger struct {
	mu    sync.Mutex
	out   *os.File
	color bool
}

type sourceLogger struct {
	app    *appLogger
	source string
}

func newAppLogger() *appLogger {
	return &appLogger{
		mu:    sync.Mutex{},
		out:   os.Stdout,
		color: supportsColor(),
	}
}

func supportsColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if term == "" || term == "dumb" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func (l *appLogger) Source(source string) sourceLogger {
	return sourceLogger{
		app:    l,
		source: source,
	}
}

func (l *appLogger) logf(level logLevel, source, format string, args ...any) {
	ts := time.Now().UTC().Format(time.RFC3339)
	msg := fmt.Sprintf(format, args...)
	levelText := fmt.Sprintf("%-5s", level)
	sourceText := fmt.Sprintf("%-8s", source)

	if l.color {
		ts = ansi("90", ts)
		levelText = ansi(levelColor(level), levelText)
		sourceText = ansi(sourceColor(source), sourceText)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.out.WriteString(ts + " " + levelText + " " + sourceText + " " + msg + "\n")
}

func (l sourceLogger) Debugf(format string, args ...any) {
	l.app.logf(logLevelDebug, l.source, format, args...)
}

func (l sourceLogger) Infof(format string, args ...any) {
	l.app.logf(logLevelInfo, l.source, format, args...)
}

func (l sourceLogger) Warnf(format string, args ...any) {
	l.app.logf(logLevelWarn, l.source, format, args...)
}

func (l sourceLogger) Errorf(format string, args ...any) {
	l.app.logf(logLevelError, l.source, format, args...)
}

func (l sourceLogger) Fatalf(format string, args ...any) {
	l.app.logf(logLevelError, l.source, format, args...)
	os.Exit(1)
}

func levelColor(level logLevel) string {
	switch level {
	case logLevelDebug:
		return "36" // cyan
	case logLevelInfo:
		return "32" // green
	case logLevelWarn:
		return "33" // yellow
	case logLevelError:
		return "31" // red
	default:
		return "37"
	}
}

func sourceColor(source string) string {
	switch source {
	case branchMain:
		return "97"
	case "api":
		return "94"
	case "registrar":
		return "35"
	case "repoBootstrap":
		return "36"
	case "imageBuilder":
		return "93"
	case "manifestRenderer":
		return "32"
	case "deployer":
		return "92"
	case "promoter":
		return "96"
	default:
		palette := []string{"34", "35", "36", "92", "93", "95", "96"}
		h := fnv.New32a()
		_, _ = h.Write([]byte(source))
		return palette[int(h.Sum32())%len(palette)]
	}
}

func ansi(code, s string) string {
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func appLoggerForProcess() *appLogger {
	return newAppLogger()
}
