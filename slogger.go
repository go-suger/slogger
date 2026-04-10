package slogger

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type SinkType string

const (
	SinkStdout SinkType = "stdout"
	SinkFile   SinkType = "file"
)

type FormatType string

const (
	FormatText   FormatType = "text"
	FormatPretty FormatType = "pretty"
	FormatJSON   FormatType = "json"
)

type SinkConfig struct {
	Type      SinkType        `json:"type" yaml:"type" toml:"type"`
	Format    FormatType      `json:"format" yaml:"format" toml:"format"`
	Level     string          `json:"level" yaml:"level" toml:"level"`
	AddSource bool            `json:"add_source" yaml:"add_source" toml:"add_source"`
	Filepath  string          `json:"filepath" yaml:"filepath" toml:"filepath"`
	Rotation  *RotationConfig `json:"rotation" yaml:"rotation" toml:"rotation"`
}

type Config struct {
	Fields map[string]any `json:"fields" yaml:"fields" toml:"fields"`
	Sinks  []SinkConfig   `json:"sinks" yaml:"sinks" toml:"sinks"`
}

type Bundle struct {
	Logger  *slog.Logger
	Closers []func() error
}

func TestLogger() *slog.Logger {
	ll := slog.New(NewPrettyTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}))

	slog.SetDefault(ll.With(Module("test")))
	return ll
}

func New(cfg Config) (*Bundle, error) {
	if len(cfg.Sinks) == 0 {
		return nil, fmt.Errorf("no sinks configured")
	}

	replaceAttr := func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			if t, ok := a.Value.Any().(time.Time); ok {
				a.Value = slog.StringValue(t.Format(time.RFC3339Nano))
			}
		}

		if a.Key == slog.LevelKey {
			a.Value = slog.StringValue(strings.ToLower(a.Value.String()))
		}

		if a.Key == "error" {
			return slog.String("error", a.Value.String())
		}

		return a
	}

	handlers := make([]slog.Handler, 0, len(cfg.Sinks))
	closers := make([]func() error, 0)

	for _, sink := range cfg.Sinks {
		w, closeFn, err := buildWriter(sink)
		if err != nil {
			return nil, err
		}
		if closeFn != nil {
			closers = append(closers, closeFn)
		}

		level, err := parseLevel(sink.Level)
		if err != nil {
			return nil, err
		}
		opts := &slog.HandlerOptions{
			Level:       level,
			AddSource:   sink.AddSource,
			ReplaceAttr: replaceAttr,
		}

		h, err := buildHandler(w, sink.Format, opts)
		if err != nil {
			for _, c := range closers {
				_ = c()
			}
			return nil, err
		}

		handlers = append(handlers, h)
	}

	var handler slog.Handler
	if len(handlers) == 1 {
		handler = handlers[0]
	} else {
		handler = slog.NewMultiHandler(handlers...)
	}

	l := slog.New(handler)

	commonArgs := buildCommonArgs(cfg)
	if len(commonArgs) > 0 {
		l = l.With(commonArgs...)
	}

	return &Bundle{
		Logger:  l,
		Closers: closers,
	}, nil
}

func (b *Bundle) Close() error {
	var errs []error
	for _, cls := range b.Closers {
		errs = append(errs, cls())
	}
	return errors.Join(errs...)
}

func buildCommonArgs(cfg Config) []any {
	args := make([]any, 0, 6+len(cfg.Fields)*2)
	keys := make([]string, 0, len(cfg.Fields))
	for k := range cfg.Fields {
		if strings.TrimSpace(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		args = append(args, k, cfg.Fields[k])
	}

	return args
}

func buildWriter(sink SinkConfig) (io.Writer, func() error, error) {
	switch sink.Type {
	case SinkStdout:
		return os.Stdout, nil, nil

	case SinkFile:
		if sink.Filepath == "" {
			return nil, nil, fmt.Errorf("file sink requires filename")
		}

		dir := filepath.Dir(sink.Filepath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, nil, fmt.Errorf("create log directory %s: %w", dir, err)
			}
		}
		if sink.Rotation != nil {
			r := sink.Rotation
			lj := &lumberjack.Logger{
				Filename:   sink.Filepath,
				MaxSize:    r.MaxSizeMB,
				MaxBackups: r.MaxBackups,
				MaxAge:     r.MaxAgeDays,
				Compress:   r.Compress,
				LocalTime:  r.LocalTime,
			}
			return lj, lj.Close, nil
		}

		f, err := os.OpenFile(sink.Filepath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file %s: %w", sink.Filepath, err)
		}

		return f, f.Close, nil

	default:
		return nil, nil, fmt.Errorf("unsupported sink type: %s", sink.Type)
	}
}

func buildHandler(w io.Writer, format FormatType, opts *slog.HandlerOptions) (slog.Handler, error) {
	switch format {
	case FormatText:
		return slog.NewTextHandler(w, opts), nil
	case FormatPretty:
		return NewPrettyTextHandler(w, opts), nil
	case FormatJSON:
		return newJSONModuleHandler(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

func parseLevel(s string) (*slog.LevelVar, error) {
	var level slog.LevelVar
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return nil, err
	}
	return &level, nil
}
