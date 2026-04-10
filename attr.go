package slogger

import (
	"log/slog"
	"strings"
)

func Module(parts ...string) slog.Attr {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return slog.String(moduleAttrKey, strings.Join(filtered, ":"))
}

func mergeModuleChain(current string, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	switch {
	case current == "":
		return next
	case next == "":
		return current
	default:
		return current + ":" + next
	}
}
