package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxLogSize = 10 * 1024 * 1024

const (
	colorBlue   = "\x1b[38;2;138;173;244m"
	colorGreen  = "\x1b[38;2;166;218;149m"
	colorYellow = "\x1b[38;2;238;212;159m"
	colorRed    = "\x1b[38;2;237;135;150m"
	colorReset  = "\x1b[0m"
)

var out io.Writer = os.Stdout

// SetOutput redirects all log lines to w.
func SetOutput(w io.Writer) {
	out = w
}

// OpenLogFile prepares the log file at path and rotates it above 10 MB.
// Lines go to the file and to the current output writer.
func OpenLogFile(path string) (*os.File, error) {
	rotate(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %w", path, err)
	}
	out = io.MultiWriter(os.Stdout, f)
	return f, nil
}

func rotate(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogSize {
		return
	}
	backup := path[:len(path)-len(filepath.Ext(path))] + ".log.1"
	os.Remove(backup)
	os.Rename(path, backup)
}

func printf(color, prefix, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(out, "%s%s %s%s\n", color, prefix, msg, colorReset)
}

// Info prints a status line.
func Info(format string, args ...any) {
	printf(colorBlue, "[i]", format, args...)
}

// Success prints a success line.
func Success(format string, args ...any) {
	printf(colorGreen, "[+]", format, args...)
}

// Warn prints a warning line.
func Warn(format string, args ...any) {
	printf(colorYellow, "[!]", format, args...)
}

// Error prints an error line.
func Error(format string, args ...any) {
	printf(colorRed, "[-]", format, args...)
}
