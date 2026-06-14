package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
)

// ANSI color codes.
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorGray   = "\033[90m"
)

type TextHandler struct {
	w         io.Writer
	level     slog.Level
	addSource bool
	color     bool
	attrs     []slog.Attr
	mu        *sync.Mutex
}

func NewTextHandler(w io.Writer, opts *slog.HandlerOptions) *TextHandler {
	level := slog.LevelInfo
	addSource := false
	if opts != nil {
		if opts.Level != nil {
			level = opts.Level.Level()
		}
		addSource = opts.AddSource
	}
	return &TextHandler{
		w:         w,
		level:     level,
		addSource: addSource,
		color:     shouldColor(w),
		mu:        &sync.Mutex{},
	}
}

func shouldColor(w io.Writer) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if _, set := os.LookupEnv("FORCE_COLOR"); set {
		return true
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func colorize(on bool, color, s string) string {
	if !on {
		return s
	}
	return color + s + colorReset
}

func levelMeta(l slog.Level) (label, color string) {
	switch l {
	case slog.LevelDebug:
		return "DEBUG", colorGray
	case slog.LevelWarn:
		return "WARN", colorYellow
	case slog.LevelError:
		return "ERROR", colorRed
	default:
		return "INFO", colorGreen
	}
}

func (h *TextHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= h.level
}

func (h *TextHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder

	label, lvlColor := levelMeta(r.Level)
	level := colorize(h.color, lvlColor, fmt.Sprintf("[%-5s]", label))
	timestamp := colorize(h.color, colorGray, r.Time.Format("2006/01/02 - 15:04:05"))
	pipe := colorize(h.color, colorGray, "|")

	fmt.Fprintf(&b, "%s %s %s %s", level, timestamp, pipe, r.Message)

	var inline []slog.Attr
	var errVal any
	collect := func(a slog.Attr) {
		a.Value = a.Value.Resolve()
		if a.Key == "" {
			return
		}
		if a.Key == "err" && errVal == nil {
			errVal = a.Value.Any()
			return
		}
		inline = append(inline, a)
	}
	for _, a := range h.attrs {
		collect(a)
	}
	r.Attrs(
		func(a slog.Attr) bool {
			collect(a)
			return true
		},
	)

	if len(inline) > 0 {
		fmt.Fprintf(&b, " %s", pipe)
		for _, a := range inline {
			fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Any())
		}
	}
	b.WriteByte('\n')

	if errVal != nil {
		writeErrChain(&b, errVal, h.color)
	}

	if h.addSource && r.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{r.PC})
		if f, _ := frames.Next(); f.File != "" {
			fmt.Fprintf(
				&b, "        %s\n",
				colorize(h.color, colorGray, fmt.Sprintf("@ %s:%d", shortFile(f.File), f.Line)),
			)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *TextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	nh := *h
	nh.attrs = make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	nh.attrs = append(nh.attrs, h.attrs...)
	nh.attrs = append(nh.attrs, attrs...)
	return &nh
}

func (h *TextHandler) WithGroup(_ string) slog.Handler {
	return h
}

func writeErrChain(b *strings.Builder, errVal any, color bool) {
	const indent = "        "
	const cont = "             "
	head := colorize(color, colorRed, "err:")
	err, ok := errVal.(error)
	if !ok {
		fmt.Fprintf(b, "%s%s %v\n", indent, head, errVal)
		return
	}
	first := true
	for err != nil {
		if first {
			fmt.Fprintf(b, "%s%s %s\n", indent, head, colorize(color, colorRed, err.Error()))
			first = false
		} else {
			fmt.Fprintf(b, "%s%s\n", cont, colorize(color, colorRed, err.Error()))
		}
		err = errors.Unwrap(err)
	}
}

func shortFile(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
