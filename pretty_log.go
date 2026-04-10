package slogger

import (
	"bytes"
	"context"
	"encoding"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fatih/color"
)

const (
	prettyTimeLayout       = "2006-01-02 15:04:05"
	prettyModuleDefault    = "main"
	prettyModuleColumnWide = 18
)

var prettyBufferPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

type PrettyTextHandler struct {
	w                 io.Writer
	mu                *sync.Mutex
	level             slog.Leveler
	addSource         bool
	sourceCut         string
	replaceAttr       func([]string, slog.Attr) slog.Attr
	preformattedAttrs []byte
	groups            []string
	groupPrefix       string
	module            string
	useColor          bool
	timeColor         *color.Color
	attrColor         *color.Color
	moduleColor       *color.Color
	debugColor        *color.Color
	infoColor         *color.Color
	warnColor         *color.Color
	errorColor        *color.Color
}

const moduleAttrKey = "_module"

type PrettyTextHandlerOptions struct {
	HandlerOptions *slog.HandlerOptions
	SourceCut      string
}

func NewPrettyTextHandler(w io.Writer, opts *slog.HandlerOptions) *PrettyTextHandler {
	return NewPrettyTextHandlerWithOptions(w, &PrettyTextHandlerOptions{
		HandlerOptions: opts,
	})
}

func NewPrettyTextHandlerWithOptions(w io.Writer, opts *PrettyTextHandlerOptions) *PrettyTextHandler {
	color.NoColor = false
	h := &PrettyTextHandler{
		w:         w,
		mu:        &sync.Mutex{},
		module:    "",
		sourceCut: "",
	}
	if usePrettyColor(w) {
		h.useColor = true
		h.timeColor = color.New(color.FgHiBlack, color.Bold)
		h.attrColor = color.New(color.FgWhite)
		h.moduleColor = color.New(color.FgGreen, color.Bold)
		h.debugColor = color.New(color.FgHiMagenta, color.Bold)
		h.infoColor = color.New(color.FgCyan, color.Bold)
		h.warnColor = color.New(color.FgYellow, color.Bold)
		h.errorColor = color.New(color.FgRed, color.Bold)
	}
	if opts != nil {
		h.sourceCut = normalizeSourceCut(opts.SourceCut)
		if opts.HandlerOptions != nil {
			h.level = opts.HandlerOptions.Level
			h.addSource = opts.HandlerOptions.AddSource
			h.replaceAttr = opts.HandlerOptions.ReplaceAttr
		}
	}
	return h
}

func (h *PrettyTextHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.level != nil {
		minLevel = h.level.Level()
	}
	return level >= minLevel
}

func (h *PrettyTextHandler) Handle(_ context.Context, r slog.Record) error {
	module := h.module
	if module == "" {
		module = prettyModuleDefault
	}

	recordAttrs := getPrettyBuffer()
	defer putPrettyBuffer(recordAttrs)
	hasRecordAttrs := false
	r.Attrs(func(attr slog.Attr) bool {
		h.appendAttr(recordAttrs, h.groups, h.groupPrefix, attr, &module, &hasRecordAttrs)
		return true
	})

	buf := getPrettyBuffer()
	defer putPrettyBuffer(buf)
	buf.Grow(128 + len(h.preformattedAttrs) + recordAttrs.Len())
	h.appendTimeLabel(buf, r.Time)
	buf.WriteByte(' ')
	h.appendLevelLabel(buf, r.Level)

	if module != "" {
		buf.WriteByte(' ')
		h.appendModuleLabel(buf, module)
		writePadding(buf, prettyModuleColumnWide-len(module)-2)
	}

	if r.Message != "" {
		buf.WriteString(r.Message)
	}

	if h.addSource || len(h.preformattedAttrs) > 0 || hasRecordAttrs {
		attrBuf := getPrettyBuffer()
		defer putPrettyBuffer(attrBuf)
		attrBuf.WriteString("  {")
		hasWrittenAttr := false
		if len(h.preformattedAttrs) > 0 {
			if hasWrittenAttr {
				attrBuf.WriteByte(' ')
			}
			attrBuf.Write(h.preformattedAttrs)
			hasWrittenAttr = true
		}
		if hasRecordAttrs {
			if hasWrittenAttr {
				attrBuf.WriteByte(' ')
			}
			attrBuf.Write(recordAttrs.Bytes())
			hasWrittenAttr = true
		}
		if h.addSource {
			h.writeSourceAttr(attrBuf, r.PC, &hasWrittenAttr)
		}
		if hasWrittenAttr {
			attrBuf.WriteByte('}')
			if h.useColor {
				buf.WriteString(h.attrColor.Sprint(attrBuf.String()))
			} else {
				buf.Write(attrBuf.Bytes())
			}
		}
	}
	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

func (h *PrettyTextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := h.clone()
	buf := getPrettyBuffer()
	defer putPrettyBuffer(buf)
	buf.Grow(len(clone.preformattedAttrs) + len(attrs)*24)
	buf.Write(clone.preformattedAttrs)
	hasWrittenAttr := buf.Len() > 0
	for _, attr := range attrs {
		clone.appendAttr(buf, clone.groups, clone.groupPrefix, attr, &clone.module, &hasWrittenAttr)
	}
	clone.preformattedAttrs = append([]byte(nil), buf.Bytes()...)
	return clone
}

func (h *PrettyTextHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := h.clone()
	clone.groups = append(clone.groups, name)
	clone.groupPrefix += name + "."
	return clone
}

func (h *PrettyTextHandler) clone() *PrettyTextHandler {
	return &PrettyTextHandler{
		w:                 h.w,
		mu:                h.mu,
		level:             h.level,
		addSource:         h.addSource,
		sourceCut:         h.sourceCut,
		replaceAttr:       h.replaceAttr,
		preformattedAttrs: append([]byte(nil), h.preformattedAttrs...),
		groups:            append([]string{}, h.groups...),
		groupPrefix:       h.groupPrefix,
		module:            h.module,
		useColor:          h.useColor,
		timeColor:         h.timeColor,
		attrColor:         h.attrColor,
		moduleColor:       h.moduleColor,
		debugColor:        h.debugColor,
		infoColor:         h.infoColor,
		warnColor:         h.warnColor,
		errorColor:        h.errorColor,
	}
}

func (h *PrettyTextHandler) appendAttr(buf *bytes.Buffer, groups []string, prefix string, attr slog.Attr, module *string, hasWrittenAttr *bool) {
	if h.replaceAttr == nil {
		h.appendAttrFast(buf, prefix, attr, module, hasWrittenAttr)
		return
	}

	attr.Value = attr.Value.Resolve()

	if attr.Equal(slog.Attr{}) {
		return
	}

	if attr.Value.Kind() == slog.KindGroup {
		nextGroups := groups
		nextPrefix := prefix
		if attr.Key != "" {
			nextGroups = appendGroups(groups, attr.Key)
			nextPrefix = prefix + attr.Key + "."
		}
		attrs := attr.Value.Group()
		for _, child := range attrs {
			h.appendAttr(buf, nextGroups, nextPrefix, child, module, hasWrittenAttr)
		}
		return
	}

	if h.replaceAttr != nil {
		attr = h.replaceAttr(groups, attr)
		attr.Value = attr.Value.Resolve()
		if attr.Equal(slog.Attr{}) {
			return
		}
	}

	key := attr.Key
	if key == moduleAttrKey {
		if s := attrValueString(attr.Value); s != "" {
			*module = mergeModuleChain(*module, s)
		}
		return
	}
	if prefix != "" {
		key = prefix + key
	}
	if key == "" {
		return
	}

	writeAttrKV(buf, key, attr.Value, hasWrittenAttr)
}

func (h *PrettyTextHandler) appendAttrFast(buf *bytes.Buffer, prefix string, attr slog.Attr, module *string, hasWrittenAttr *bool) {
	attr.Value = attr.Value.Resolve()

	if attr.Equal(slog.Attr{}) {
		return
	}

	if attr.Value.Kind() == slog.KindGroup {
		nextPrefix := prefix
		if attr.Key != "" {
			nextPrefix = prefix + attr.Key + "."
		}
		for _, child := range attr.Value.Group() {
			h.appendAttrFast(buf, nextPrefix, child, module, hasWrittenAttr)
		}
		return
	}

	key := attr.Key
	if key == moduleAttrKey {
		if s := attrValueString(attr.Value); s != "" {
			*module = mergeModuleChain(*module, s)
		}
		return
	}
	if prefix != "" {
		key = prefix + key
	}
	if key == "" {
		return
	}

	writeAttrKV(buf, key, attr.Value, hasWrittenAttr)
}

func prettyLevel(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "DBG"
	case level < slog.LevelWarn:
		return "INF"
	case level < slog.LevelError:
		return "WRN"
	default:
		return "ERR"
	}
}

func usePrettyColor(w io.Writer) bool {
	return w == os.Stdout || w == os.Stderr
}

func (h *PrettyTextHandler) appendTimeLabel(buf *bytes.Buffer, t time.Time) {
	if !h.useColor {
		buf.WriteByte('[')
		appendTime(buf, t)
		buf.WriteByte(']')
		return
	}

	var tb [32]byte
	buf.WriteString(h.timeColor.Sprint("[" + string(t.AppendFormat(tb[:0], prettyTimeLayout)) + "]"))
}

func (h *PrettyTextHandler) appendLevelLabel(buf *bytes.Buffer, level slog.Level) {
	label := prettyLevel(level)
	if !h.useColor {
		buf.WriteByte('[')
		buf.WriteString(label)
		buf.WriteByte(']')
		return
	}

	var c *color.Color
	switch {
	case level <= slog.LevelDebug:
		c = h.debugColor
	case level < slog.LevelWarn:
		c = h.infoColor
	case level < slog.LevelError:
		c = h.warnColor
	default:
		c = h.errorColor
	}
	buf.WriteString(c.Sprint("[" + label + "]"))
}

func (h *PrettyTextHandler) appendModuleLabel(buf *bytes.Buffer, module string) {
	if !h.useColor {
		buf.WriteByte('<')
		buf.WriteString(module)
		buf.WriteByte('>')
		return
	}

	buf.WriteString(h.moduleColor.Sprint("<" + module + ">"))
}

func (h *PrettyTextHandler) writeSourceAttr(buf *bytes.Buffer, pc uintptr, hasWrittenAttr *bool) {
	if pc == 0 {
		return
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	if frame.File == "" {
		return
	}

	if *hasWrittenAttr {
		buf.WriteByte(' ')
	} else {
		*hasWrittenAttr = true
	}
	buf.WriteString(slog.SourceKey)
	buf.WriteByte('=')
	appendShortSource(buf, frame.File, h.sourceCut)
	buf.WriteByte(':')
	appendInt64(buf, int64(frame.Line))
}

func appendShortSource(buf *bytes.Buffer, file string, sourceCut string) {
	file = filepath.ToSlash(file)
	if sourceCut != "" {
		if idx := strings.Index(file, sourceCut); idx >= 0 {
			buf.WriteString(file[idx:])
			return
		}
	}
	last := strings.LastIndexByte(file, '/')
	if last < 0 {
		buf.WriteString(file)
		return
	}
	prev := strings.LastIndexByte(file[:last], '/')
	if prev >= 0 {
		buf.WriteString(file[prev+1:])
		return
	}
	buf.WriteString(file[last+1:])
}

func appendTime(buf *bytes.Buffer, t time.Time) {
	var tb [32]byte
	buf.Write(t.AppendFormat(tb[:0], prettyTimeLayout))
}

func writeAttrKV(buf *bytes.Buffer, key string, value slog.Value, hasWrittenAttr *bool) {
	if *hasWrittenAttr {
		buf.WriteByte(' ')
	} else {
		*hasWrittenAttr = true
	}
	buf.WriteString(key)
	buf.WriteByte('=')
	appendPrettyValue(buf, value)
}

func appendPrettyValue(buf *bytes.Buffer, v slog.Value) {
	switch v.Kind() {
	case slog.KindString:
		appendPrettyString(buf, v.String())
	case slog.KindBool:
		if v.Bool() {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case slog.KindDuration:
		buf.WriteString(v.Duration().String())
	case slog.KindFloat64:
		appendFloat64(buf, v.Float64())
	case slog.KindInt64:
		appendInt64(buf, v.Int64())
	case slog.KindUint64:
		appendUint64(buf, v.Uint64())
	case slog.KindTime:
		appendTime(buf, v.Time())
	case slog.KindAny:
		appendPrettyAny(buf, v.Any())
	default:
		buf.WriteString(v.String())
	}
}

func appendPrettyAny(buf *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		buf.WriteString("<nil>")
	case string:
		appendPrettyString(buf, x)
	case []byte:
		buf.WriteString(strconv.Quote(string(x)))
	case error:
		appendPrettyString(buf, x.Error())
	case fmt.Stringer:
		appendPrettyString(buf, x.String())
	case encoding.TextMarshaler:
		data, err := x.MarshalText()
		if err != nil {
			appendPrettyString(buf, err.Error())
			return
		}
		appendPrettyString(buf, string(data))
	default:
		if bs, ok := byteSlice(v); ok {
			buf.WriteString(strconv.Quote(string(bs)))
			return
		}
		appendPrettyString(buf, fmt.Sprint(v))
	}
}

func appendPrettyString(buf *bytes.Buffer, s string) {
	if needsQuotingPretty(s) {
		buf.WriteString(strconv.Quote(s))
		return
	}
	buf.WriteString(s)
}

func attrValueString(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindFloat64:
		return strconv.FormatFloat(v.Float64(), 'g', -1, 64)
	case slog.KindInt64:
		return strconv.FormatInt(v.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(v.Uint64(), 10)
	case slog.KindTime:
		return v.Time().Format(prettyTimeLayout)
	case slog.KindAny:
		return fmt.Sprint(v.Any())
	default:
		return v.String()
	}
}

func appendInt64(buf *bytes.Buffer, v int64) {
	var nb [20]byte
	buf.Write(strconv.AppendInt(nb[:0], v, 10))
}

func appendUint64(buf *bytes.Buffer, v uint64) {
	var nb [20]byte
	buf.Write(strconv.AppendUint(nb[:0], v, 10))
}

func appendFloat64(buf *bytes.Buffer, v float64) {
	var nb [32]byte
	buf.Write(strconv.AppendFloat(nb[:0], v, 'g', -1, 64))
}

func writePadding(buf *bytes.Buffer, n int) {
	if n < 1 {
		n = 1
	}
	const spaces = "                                "
	for n > len(spaces) {
		buf.WriteString(spaces)
		n -= len(spaces)
	}
	buf.WriteString(spaces[:n])
}

func appendGroups(groups []string, name string) []string {
	next := make([]string, len(groups)+1)
	copy(next, groups)
	next[len(groups)] = name
	return next
}

func getPrettyBuffer() *bytes.Buffer {
	buf := prettyBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

func putPrettyBuffer(buf *bytes.Buffer) {
	if buf.Cap() > 64<<10 {
		return
	}
	prettyBufferPool.Put(buf)
}

func normalizeSourceCut(v string) string {
	v = filepath.ToSlash(strings.TrimSpace(v))
	v = strings.Trim(v, "/")
	return v
}

func needsQuotingPretty(s string) bool {
	if len(s) == 0 {
		return true
	}
	for i := 0; i < len(s); {
		b := s[i]
		if b < utf8.RuneSelf {
			if b == ' ' || b == '=' || b == '"' || b == '{' || b == '}' || !safeSetPretty[b] {
				return true
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return true
		}
		i += size
	}
	return false
}

func byteSlice(a any) ([]byte, bool) {
	if bs, ok := a.([]byte); ok {
		return bs, true
	}
	t := reflect.TypeOf(a)
	if t != nil && t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return reflect.ValueOf(a).Bytes(), true
	}
	return nil, false
}

var safeSetPretty = func() [utf8.RuneSelf]bool {
	var safe [utf8.RuneSelf]bool
	for i := 0; i < len(safe); i++ {
		switch {
		case i >= 'a' && i <= 'z':
			safe[i] = true
		case i >= 'A' && i <= 'Z':
			safe[i] = true
		case i >= '0' && i <= '9':
			safe[i] = true
		case strings.ContainsRune("!#$%&'()*+,-./:;<>?@[\\]^_`|~", rune(i)):
			safe[i] = true
		}
	}
	return safe
}()
