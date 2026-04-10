package slogger

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
	"gopkg.in/natefinch/lumberjack.v2"
)

func TestName(t *testing.T) {
	bundle, err := New(Config{
		Fields: map[string]any{
			"region": "cn-shanghai",
			"node":   "node-01",
		},
		Sinks: []SinkConfig{
			//{
			//	Type:      SinkStdout,
			//	Format:    FormatText,
			//	Level:     "debug",
			//	AddSource: true,
			//	Filepath:  "",
			//},
			{
				Type:      SinkStdout,
				Format:    FormatPretty,
				Level:     "debug",
				AddSource: true,
				Filepath:  "",
			},
			{
				Type:      SinkFile,
				Format:    FormatJSON,
				Level:     "debug",
				AddSource: true,
				Filepath:  "./logs/gc.log",
			},
		},
	})
	if err != nil {
		t.Fatalf("logger.New() failed: %v", err)
	}
	slog.SetDefault(bundle.Logger)
	color.NoColor = false

	slog.Info("http server listening")

	log := slog.With(Module("puller", "core"))
	log.Info("http server listening")
	log.Warn("http server listening")
	log.Debug("http server listening")
	log.Error("http server listening")

	log.Info("group", slog.Group("g1", slog.String("name", "ass")))
}

func TestBuildHandlerUsesPrettyTextHandler(t *testing.T) {
	h, err := buildHandler(&bytes.Buffer{}, FormatPretty, &slog.HandlerOptions{})
	if err != nil {
		t.Fatalf("buildHandler() failed: %v", err)
	}

	if _, ok := h.(*PrettyTextHandler); !ok {
		t.Fatalf("buildHandler() returned %T, want *PrettyTextHandler", h)
	}
}

func TestBuildHandlerUsesStandardTextHandler(t *testing.T) {
	h, err := buildHandler(&bytes.Buffer{}, FormatText, &slog.HandlerOptions{})
	if err != nil {
		t.Fatalf("buildHandler() failed: %v", err)
	}

	if _, ok := h.(*PrettyTextHandler); ok {
		t.Fatalf("buildHandler() returned %T, want standard text handler", h)
	}
}

func TestJSONHandlerKeepsSingleModuleField(t *testing.T) {
	var buf bytes.Buffer
	h, err := buildHandler(&buf, FormatJSON, &slog.HandlerOptions{Level: slog.LevelDebug})
	if err != nil {
		t.Fatalf("buildHandler() failed: %v", err)
	}
	logger := slog.New(h).With(
		Module("puller"),
		Module("core"),
		"service", "a3",
	)
	logger.Info("http server listening", Module("worker"))

	var obj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("json unmarshal failed: %v, raw=%s", err, buf.String())
	}

	if got, ok := obj[moduleAttrKey].(string); !ok || got != "puller:core:worker" {
		t.Fatalf("unexpected %s: %#v", moduleAttrKey, obj[moduleAttrKey])
	}
	if got, ok := obj["service"].(string); !ok || got != "a3" {
		t.Fatalf("unexpected service: %#v", obj["service"])
	}
}

func TestPrettyTextHandlerChainsModule(t *testing.T) {
	var buf bytes.Buffer

	handler := NewPrettyTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}).WithAttrs([]slog.Attr{
		Module("puller"),
		Module("core"),
	})

	record := slog.NewRecord(
		time.Date(2026, 3, 20, 12, 45, 54, 0, time.Local),
		slog.LevelInfo,
		"http server listening",
		0,
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() failed: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "<puller:core>") {
		t.Fatalf("unexpected module label: %s", got)
	}
}

func TestBuildWriterUsesLumberjackWhenRotationConfigured(t *testing.T) {
	filepath := filepath.Join(t.TempDir(), "app.log")
	w, closeFn, err := buildWriter(SinkConfig{
		Type:     SinkFile,
		Filepath: filepath,
		Rotation: &RotationConfig{
			MaxSizeMB:  10,
			MaxBackups: 3,
			MaxAgeDays: 7,
			Compress:   true,
			LocalTime:  true,
		},
	})
	if err != nil {
		t.Fatalf("buildWriter() failed: %v", err)
	}
	if closeFn == nil {
		t.Fatalf("buildWriter() closeFn is nil")
	}

	lj, ok := w.(*lumberjack.Logger)
	if !ok {
		t.Fatalf("buildWriter() writer type = %T, want *lumberjack.Logger", w)
	}
	if lj.Filename != filepath {
		t.Fatalf("lumberjack filename = %s, want %s", lj.Filename, filepath)
	}

	if err := closeFn(); err != nil {
		t.Fatalf("closeFn() failed: %v", err)
	}
}

func TestBuildWriterUsesFileWhenRotationNotConfigured(t *testing.T) {
	filepath := filepath.Join(t.TempDir(), "app.log")
	w, closeFn, err := buildWriter(SinkConfig{
		Type:     SinkFile,
		Filepath: filepath,
		Rotation: nil,
	})
	if err != nil {
		t.Fatalf("buildWriter() failed: %v", err)
	}
	if closeFn == nil {
		t.Fatalf("buildWriter() closeFn is nil")
	}

	if _, ok := w.(*os.File); !ok {
		t.Fatalf("buildWriter() writer type = %T, want *os.File", w)
	}

	if err := closeFn(); err != nil {
		t.Fatalf("closeFn() failed: %v", err)
	}
}

func TestPrettyTextHandlerFormatsOutput(t *testing.T) {
	var buf bytes.Buffer

	handler := NewPrettyTextHandler(&buf, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	}).WithAttrs([]slog.Attr{
		Module("puller", "core"),
		slog.String("service", "a3"),
		slog.String("version", "8.1.3"),
	})

	record := slog.NewRecord(
		time.Date(2026, 3, 20, 12, 45, 54, 0, time.Local),
		slog.LevelDebug,
		"http server listening",
		testPC(),
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() failed: %v", err)
	}
}

func TestPrettyTextHandlerWithGroupNamespacesAttrs(t *testing.T) {
	var buf bytes.Buffer

	handler := NewPrettyTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}).WithGroup("puller").WithAttrs([]slog.Attr{
		slog.String("service", "a3"),
	})

	record := slog.NewRecord(
		time.Date(2026, 3, 20, 12, 45, 54, 0, time.Local),
		slog.LevelInfo,
		"http server listening",
		0,
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() failed: %v", err)
	}

	got := buf.String()
	want := "[2026-03-20 12:45:54] [INF] <main>            http server listening  {puller.service=a3}\n"
	if got != want {
		t.Fatalf("unexpected output:\n%s", got)
	}
}

func TestPrettyTextHandlerUsesDefaultModulePadding(t *testing.T) {
	var buf bytes.Buffer

	handler := NewPrettyTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	record := slog.NewRecord(
		time.Date(2026, 3, 20, 12, 45, 54, 0, time.Local),
		slog.LevelInfo,
		"http server listening",
		0,
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() failed: %v", err)
	}

	got := buf.String()
	want := "[2026-03-20 12:45:54] [INF] <main>            http server listening\n"
	if got != want {
		t.Fatalf("unexpected output:\n%s", got)
	}
}

func TestPrettyTextHandlerColorsWholeAttrBlockWithAttrColor(t *testing.T) {
	color.NoColor = false
	defer func() { color.NoColor = false }()

	var buf bytes.Buffer
	baseHandler := NewPrettyTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	baseHandler.w = &buf
	handler := baseHandler.WithAttrs([]slog.Attr{
		slog.String("k", "v"),
	})

	record := slog.NewRecord(
		time.Date(2026, 3, 20, 12, 45, 54, 0, time.Local),
		slog.LevelInfo,
		"http server listening",
		0,
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() failed: %v", err)
	}

	got := buf.String()
	wantColoredBlock := baseHandler.attrColor.Sprint("  {k=v}")
	if !strings.Contains(got, wantColoredBlock) {
		t.Fatalf("attr block is not fully colored with time color, got:\n%s", got)
	}
}

func TestAppendShortSourceUsesConfiguredCut(t *testing.T) {
	handler := NewPrettyTextHandlerWithOptions(&bytes.Buffer{}, &PrettyTextHandlerOptions{
		SourceCut: "slogger",
	})

	var buf bytes.Buffer
	handler.writeSourceAttr(&buf, testPC(), new(bool))

	got := buf.String()
	if !regexp.MustCompile(`^source=slogger/slogger_test\.go:\d+$`).MatchString(got) {
		t.Fatalf("unexpected source output: %s", got)
	}
}

func TestAppendShortSourceFallsBackWithoutCut(t *testing.T) {
	handler := NewPrettyTextHandler(&bytes.Buffer{}, nil)

	var buf bytes.Buffer
	handler.writeSourceAttr(&buf, testPC(), new(bool))

	got := buf.String()
	if !regexp.MustCompile(`^source=slogger/slogger_test\.go:\d+$`).MatchString(got) {
		t.Fatalf("unexpected source output: %s", got)
	}
}

func TestPrettyTextHandlerPlacesSourceLast(t *testing.T) {
	var buf bytes.Buffer

	handler := NewPrettyTextHandlerWithOptions(&buf, &PrettyTextHandlerOptions{
		HandlerOptions: &slog.HandlerOptions{
			Level:     slog.LevelInfo,
			AddSource: true,
		},
		SourceCut: "slogger",
	}).WithAttrs([]slog.Attr{
		slog.String("service", "a3"),
	})

	record := slog.NewRecord(
		time.Date(2026, 3, 20, 12, 45, 54, 0, time.Local),
		slog.LevelInfo,
		"http server listening",
		testPC(),
	)
	record.AddAttrs(slog.Int("port", 8080))

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() failed: %v", err)
	}

	got := buf.String()
	re := regexp.MustCompile(`\{service=a3 port=8080 source=slogger/slogger_test\.go:\d+\}`)
	if !re.MatchString(got) {
		t.Fatalf("source is not the last attr, got:\n%s", got)
	}
}

func BenchmarkPrettyTextHandler(b *testing.B) {
	logger := slog.New(NewPrettyTextHandler(io.Discard, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})).With(
		Module("puller", "core"),
		"service", "a3",
		"version", "8.1.3",
	)

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.LogAttrs(ctx, slog.LevelDebug, "http server listening",
			slog.Int("port", 8080),
			slog.String("addr", "0.0.0.0"),
			slog.Group("http", slog.String("proto", "tcp")),
		)
	}
}

func BenchmarkStandardTextHandler(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})).With(
		"service", "a3",
		"version", "8.1.3",
	)

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.LogAttrs(ctx, slog.LevelDebug, "http server listening",
			slog.Int("port", 8080),
			slog.String("addr", "0.0.0.0"),
			slog.Group("http", slog.String("proto", "tcp")),
		)
	}
}

func testPC() uintptr {
	pc, _, _, ok := runtime.Caller(1)
	if !ok {
		return 0
	}
	return pc
}
