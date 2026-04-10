package slogger

import (
	"context"
	"log/slog"
)

type jsonModuleHandler struct {
	next      slog.Handler
	module    string
	hasModule bool
}

func newJSONModuleHandler(next slog.Handler) slog.Handler {
	return &jsonModuleHandler{next: next}
}

func (h *jsonModuleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *jsonModuleHandler) Handle(ctx context.Context, r slog.Record) error {
	module := h.module
	hasModule := h.hasModule

	record := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(attr slog.Attr) bool {
		if attr.Key == moduleAttrKey {
			if s := attrValueString(attr.Value); s != "" {
				module = mergeModuleChain(module, s)
				hasModule = true
			}
			return true
		}
		record.AddAttrs(attr)
		return true
	})
	if hasModule {
		record.AddAttrs(slog.String(moduleAttrKey, module))
	}
	return h.next.Handle(ctx, record)
}

func (h *jsonModuleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	module := h.module
	hasModule := h.hasModule
	filtered := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Key == moduleAttrKey {
			if s := attrValueString(attr.Value); s != "" {
				module = mergeModuleChain(module, s)
				hasModule = true
			}
			continue
		}
		filtered = append(filtered, attr)
	}
	return &jsonModuleHandler{
		next:      h.next.WithAttrs(filtered),
		module:    module,
		hasModule: hasModule,
	}
}

func (h *jsonModuleHandler) WithGroup(name string) slog.Handler {
	return &jsonModuleHandler{
		next:      h.next.WithGroup(name),
		module:    h.module,
		hasModule: h.hasModule,
	}
}
