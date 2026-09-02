package termcolor

import (
	"fmt"
	"os"
)

const (
	reset  = "\x1b[0m"
	Red    = "\x1b[31m"
	Green  = "\x1b[32m"
	Yellow = "\x1b[33m"
	Cyan   = "\x1b[36m"
	Dim    = "\x1b[2m"
	Bold   = "\x1b[1m"
)

func Enabled(noColorFlag bool) bool {
	if noColorFlag || os.Getenv("NO_COLOR") != "" {
		return false
	}
	return IsTerminal(os.Stdout)
}

func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func Wrap(on bool, code, text string) string {
	if !on || code == "" {
		return text
	}
	return code + text + reset
}

func Status(status int, on bool) string {
	text := fmt.Sprintf("%3d", status)
	if !on {
		return text
	}
	switch {
	case status >= 500:
		return Wrap(true, Red, text)
	case status >= 400:
		return Wrap(true, Yellow, text)
	case status >= 300:
		return Wrap(true, Cyan, text)
	default:
		return Wrap(true, Green, text)
	}
}
