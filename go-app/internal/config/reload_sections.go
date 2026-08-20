package config

import (
	"reflect"
	"sort"
	"strings"
	"sync"
)

// ================================================================================
// Config section introspection (INF-A slice 1)
// ================================================================================
// Reloadable components declare which top-level config sections they care
// about (Reloadable.RelevantSections). The reloader needs to answer "did any
// of those sections actually change?" without being handed a pre-computed
// ConfigDiff — ConfigReloader.ReloadAll receives the old and new *Config and
// nothing else, and the rollback path re-reloads with the previous config
// where no diff was ever computed.
//
// Section names are the `mapstructure` tag names of Config's own fields, i.e.
// exactly the YAML keys an operator edits ("database", "redis", "log",
// "metrics", "llm", ...). This is deliberately the same vocabulary
// DefaultConfigComparator.fieldToComponent splits out of its dotted field
// paths, so a component's RelevantSections and the diff's affected-component
// names stay in sync by construction.

// sectionFieldIndex maps a mapstructure section name to the index of the
// corresponding Config struct field. Built once per process.
var sectionFieldIndex = sync.OnceValue(func() map[string]int {
	index := make(map[string]int)
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		name := sectionNameOf(field)
		if name == "" {
			continue
		}
		index[name] = i
	}
	return index
})

// sectionNameOf resolves the mapstructure section name of a Config field.
// Returns "" for fields that carry no section identity of their own
// (mapstructure:"-", e.g. Config.Routing, which is populated by
// loadRouteConfig rather than by viper).
func sectionNameOf(field reflect.StructField) string {
	tag, ok := field.Tag.Lookup("mapstructure")
	if !ok {
		return strings.ToLower(field.Name)
	}
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	if name == "" {
		return strings.ToLower(field.Name)
	}
	return name
}

// KnownSections returns every top-level config section name, sorted. Used by
// tests and by the reloader's registration-time validation of a component's
// RelevantSections (a typo there would silently make the component never
// reload, which is the worst possible failure mode for a hot-reload hook).
func KnownSections() []string {
	index := sectionFieldIndex()
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsKnownSection reports whether name is a top-level config section.
func IsKnownSection(name string) bool {
	_, ok := sectionFieldIndex()[name]
	return ok
}

// SectionChanged reports whether the named top-level config section differs
// between oldCfg and newCfg.
//
// Semantics chosen for the hot-reload path:
//   - An unknown section name always reports true. A component asking about a
//     section that does not exist must not be silently skipped; it will be
//     reloaded (and its own Reload can then no-op), and Register logs the
//     mismatch loudly.
//   - A nil oldCfg reports true: there is no previous state to compare
//     against, so every component is considered affected (this is the
//     "reload everything" case).
//   - Config.Routing is NOT a section of its own (mapstructure:"-"). Route
//     tree changes reach components through ServiceRegistry.ReloadConfig's
//     own routing path, not through this comparison — see
//     routing_fingerprint.go for why a route-only edit is still visible to
//     the coordinator's diff.
func SectionChanged(oldCfg, newCfg *Config, section string) bool {
	if oldCfg == nil || newCfg == nil {
		return true
	}
	idx, ok := sectionFieldIndex()[section]
	if !ok {
		return true
	}
	oldValue := reflect.ValueOf(*oldCfg).Field(idx).Interface()
	newValue := reflect.ValueOf(*newCfg).Field(idx).Interface()
	return !reflect.DeepEqual(oldValue, newValue)
}

// ChangedSections returns the sorted names of every top-level section that
// differs between oldCfg and newCfg. Returns every known section when either
// config is nil (same "reload everything" convention as SectionChanged).
func ChangedSections(oldCfg, newCfg *Config) []string {
	if oldCfg == nil || newCfg == nil {
		return KnownSections()
	}

	oldValue := reflect.ValueOf(*oldCfg)
	newValue := reflect.ValueOf(*newCfg)

	changed := make([]string, 0, 4)
	for name, idx := range sectionFieldIndex() {
		if !reflect.DeepEqual(oldValue.Field(idx).Interface(), newValue.Field(idx).Interface()) {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}
