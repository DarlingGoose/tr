package textractor

import "strings"

type HookFilter struct {
	// Allow only these hook groups. Empty means allow all.
	AllowGroups map[string]struct{}

	// Drop these hook groups.
	DenyGroups map[string]struct{}

	// Drop gdi32/text-measurement hooks.
	IgnoreNoiseHooks bool
}

func NewHookFilter(allowGroups ...string) HookFilter {
	f := HookFilter{
		AllowGroups:      make(map[string]struct{}, len(allowGroups)),
		DenyGroups:       map[string]struct{}{},
		IgnoreNoiseHooks: true,
	}

	for _, group := range allowGroups {
		group = strings.TrimSpace(group)
		if group != "" {
			f.AllowGroups[group] = struct{}{}
		}
	}

	return f
}

func (f HookFilter) Allow(line *Line) bool {
	if line == nil {
		return false
	}

	group := line.HookGroup()

	if f.IgnoreNoiseHooks && IsNoiseHook(line) {
		return false
	}

	if _, denied := f.DenyGroups[group]; denied {
		return false
	}

	if len(f.AllowGroups) > 0 {
		_, allowed := f.AllowGroups[group]
		return allowed
	}

	return true
}
