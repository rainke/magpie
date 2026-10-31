package agent

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/codexcat"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

// singleDirectState keeps the settings displaced by an explicit direct binding.
// Last is also used to leave subsequent edits by the user alone on restore.
type singleDirectState struct {
	Provider string
	Before   map[string]*string
	Last     map[string]*string
}

func singleDirect(a *Agent, at place, proto provider.Protocol) {
	id, path := a.ID, a.Path
	key := at.key(id + ".direct.single")
	a.Direct = []provider.Protocol{proto}
	load := func() *singleDirectState {
		var s singleDirectState
		if json.Unmarshal([]byte(stashLoad()[key]), &s) != nil || s.Provider == "" {
			return nil
		}
		return &s
	}
	save := func(s *singleDirectState) { b, _ := json.Marshal(s); stash(map[string]string{key: string(b)}) }
	jsonField := func(k string) bool { return id == "claude" && (k == "modelPicker" || k == "availableModels") }
	read := func(k string) *string {
		var v string
		var ok bool
		if jsonField(k) {
			v, ok = edit.GetJSONRaw(path, k)
		} else if id == "claude" {
			v, ok = edit.GetJSON(path, k)
		} else {
			v, ok = edit.GetTOMLTop(path, k)
		}
		if !ok {
			return nil
		}
		return &v
	}
	write := func(k string, v *string) error {
		if id == "claude" {
			if v == nil {
				return edit.DelJSON(path, k)
			}
			if jsonField(k) {
				return edit.SetJSON(path, edit.KV{Path: k, Value: json.RawMessage(*v)})
			}
			return edit.SetJSON(path, edit.KV{Path: k, Value: *v})
		}
		if v == nil {
			return edit.DelTOMLTop(path, k)
		}
		return edit.SetTOMLTop(path, edit.KV{Path: k, Value: *v})
	}
	same := func(x, y *string) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	sameAt := func(k string, x, y *string) bool {
		if jsonField(k) && x != nil && y != nil {
			return sameJSON(*x, json.RawMessage(*y))
		}
		return same(x, y)
	}
	keys := []string{"model", "model_provider", "model_catalog_json", "openai_base_url"}
	if id == "claude" {
		keys = append([]string{"model", "modelPicker", "availableModels", "env.ANTHROPIC_API_KEY"}, func() []string {
			var out []string
			for _, k := range claudeEnv {
				out = append(out, "env."+k)
			}
			return out
		}()...)
	}
	restore := func() error {
		s := load()
		if s == nil {
			return nil
		}
		baseChanged := id == "claude" && !same(read("env.ANTHROPIC_BASE_URL"), s.Last["env.ANTHROPIC_BASE_URL"])
		routeChanged := baseChanged || id == "claude" && (!same(read("env.ANTHROPIC_API_KEY"), s.Last["env.ANTHROPIC_API_KEY"]) || !same(read("env.ANTHROPIC_AUTH_TOKEN"), s.Last["env.ANTHROPIC_AUTH_TOKEN"])) || id == "codex" && !same(read("model_provider"), s.Last["model_provider"])
		if id == "codex" && !routeChanged {
			table, err := edit.GetTOMLTable(path, "model_providers."+singleDirectID(s.Provider))
			if err != nil {
				return err
			}
			routeChanged = table["base_url"] != valueOf(s.Last["@base"]) || table["experimental_bearer_token"] != valueOf(s.Last["@token"])
		}
		if baseChanged && same(read("env.ANTHROPIC_API_KEY"), s.Last["env.ANTHROPIC_API_KEY"]) {
			// Never put the gateway's old token on a new endpoint.
			if err := write("env.ANTHROPIC_API_KEY", nil); err != nil {
				return err
			}
		}
		// A picker edited while direct belongs to the user, even if the
		// gateway's old picker was marked as magpie's before the binding.
		if id == "claude" && s.Before != nil {
			if _, tracked := s.Before["modelPicker"]; tracked && !sameAt("modelPicker", read("modelPicker"), s.Last["modelPicker"]) {
				forget(at.key("claude.model_picker"))
			}
		}
		for _, k := range keys {
			if routeChanged {
				continue
			}
			if sameAt(k, read(k), s.Last[k]) {
				if err := write(k, s.Before[k]); err != nil {
					return err
				}
			}
		}
		if id == "codex" {
			// A hand-edited provider table belongs to the user.
			table, err := edit.GetTOMLTable(path, "model_providers."+singleDirectID(s.Provider))
			if err != nil {
				return err
			}
			name := "model_providers." + singleDirectID(s.Provider)
			// If the active direct table was edited, it is the user's route now.
			if valueOf(read("model_provider")) == singleDirectID(s.Provider) && (table["base_url"] != valueOf(s.Last["@base"]) || table["experimental_bearer_token"] != valueOf(s.Last["@token"])) {
				forget(key)
				return nil
			}
			for k, last := range map[string]string{"base_url": valueOf(s.Last["@base"]), "experimental_bearer_token": valueOf(s.Last["@token"]), "wire_api": "responses", "name": valueOf(s.Last["@name"])} {
				if v, ok := table[k]; ok && v == last {
					if err := edit.DelTOMLKey(path, name, k); err != nil {
						return err
					}
				}
			}
		}
		forget(key)
		return nil
	}
	previousSync, previousUnwire, previousJoined, previousCheck := a.Sync, a.Unwire, a.Joined, a.Check
	a.Joined = func() bool { return load() != nil || previousJoined != nil && previousJoined() }
	a.Owned = func() bool { return load() != nil }
	a.DirectActive = a.Owned
	a.Check = func() string {
		if s := load(); s != nil {
			if err := provider.FileError(); err != nil {
				return err.Error()
			}
			for _, k := range []string{"env.ANTHROPIC_BASE_URL", "env.ANTHROPIC_API_KEY", "env.ANTHROPIC_AUTH_TOKEN", "modelPicker", "availableModels"} {
				_, tracked := s.Before[k]
				if id == "claude" && (!jsonField(k) || tracked) && !sameAt(k, read(k), s.Last[k]) {
					return "Claude Code's direct provider settings were changed"
				}
			}
			if id == "codex" {
				t, _ := edit.GetTOMLTable(path, "model_providers."+singleDirectID(s.Provider))
				if valueOf(read("model_provider")) != singleDirectID(s.Provider) || t["base_url"] != valueOf(s.Last["@base"]) || t["experimental_bearer_token"] != valueOf(s.Last["@token"]) {
					return "Codex's direct provider settings were changed"
				}
			}
			return ""
		}
		if previousCheck != nil {
			return previousCheck()
		}
		return ""
	}
	a.Unwire = func() error {
		if err := restore(); err != nil {
			return err
		}
		if previousUnwire != nil {
			return previousUnwire()
		}
		return nil
	}
	a.Sync = func() error {
		if err := provider.FileError(); err != nil {
			return err
		}
		s := load()
		entries := directEntries(id, a.Direct)
		if s != nil && (len(entries) == 0 || entries[0].p.ID != s.Provider) {
			if err := restore(); err != nil {
				return err
			}
			s = nil
		}
		if len(entries) == 0 || s == nil && (!a.Wired() || a.Disconnected()) {
			if previousSync != nil {
				return previousSync()
			}
			return nil
		}
		e := entries[0]
		models := e.p.DirectModels(proto)
		if len(models) == 0 {
			return nil
		}
		fresh := s == nil
		if fresh {
			if id == "codex" {
				t, err := edit.GetTOMLTable(path, "model_providers."+singleDirectID(e.p.ID))
				if err != nil {
					return err
				}
				if len(t) != 0 {
					return fmt.Errorf("Codex already has a provider named %s", singleDirectID(e.p.ID))
				}
			}
			s = &singleDirectState{Provider: e.p.ID, Before: map[string]*string{}, Last: map[string]*string{}}
			for _, k := range keys {
				s.Before[k] = read(k)
			}
			save(s)
		} else {
			// Bindings made before the picker was managed have no snapshots
			// for these fields. Capture them once before writing the list.
			if id == "claude" {
				if s.Before == nil {
					s.Before = map[string]*string{}
				}
				if s.Last == nil {
					s.Last = map[string]*string{}
				}
				for _, k := range []string{"modelPicker", "availableModels"} {
					if _, tracked := s.Before[k]; !tracked {
						s.Before[k] = read(k)
						s.Last[k] = read(k)
					}
				}
				save(s)
			}
			if a.Check() != "" {
				return nil
			}
		}
		if id == "claude" {
			var err error
			models, err = claudeDirectAllowed(models, e.p.ID, s.Before["availableModels"])
			if err != nil {
				return err
			}
		}
		current := valueOf(read("model"))
		current = strings.TrimPrefix(current, e.p.ID+"/")
		if len(models) > 0 && !slices.ContainsFunc(models, func(m catalog.Model) bool { return m.ID == current }) {
			current = models[0].ID
		}
		set := func(k string, v *string) error {
			if !fresh && !sameAt(k, read(k), s.Last[k]) {
				return nil
			}
			if !sameAt(k, read(k), v) {
				if err := write(k, v); err != nil {
					return err
				}
			}
			s.Last[k] = read(k)
			save(s)
			return nil
		}
		str := func(v string) *string { return &v }
		if id == "claude" {
			want := map[string]*string{}
			for _, k := range keys {
				want[k] = nil
			}
			for k, v := range map[string]string{"model": current, "env.ANTHROPIC_BASE_URL": directBase(e.p, proto), "env.ANTHROPIC_API_KEY": e.p.Key, "env.ANTHROPIC_DEFAULT_OPUS_MODEL": current, "env.ANTHROPIC_DEFAULT_SONNET_MODEL": current, "env.ANTHROPIC_DEFAULT_HAIKU_MODEL": current, "env.ANTHROPIC_DEFAULT_FABLE_MODEL": current, "env.ANTHROPIC_SMALL_FAST_MODEL": current} {
				want[k] = str(v)
			}
			// Claude Code otherwise appends the old gateway picker and its
			// built-in rows. Its allowlist also covers /model <id>.
			rows := make([]map[string]string, 0, len(models))
			ids := make([]string, 0, len(models))
			for _, m := range models {
				rows = append(rows, map[string]string{"model": m.ID, "label": m.Name, "description": e.p.Name})
				ids = append(ids, m.ID)
			}
			picker, err := json.Marshal(map[string]any{"replaceBuiltInOptions": true, "options": rows})
			if err != nil {
				return err
			}
			allowed, err := json.Marshal(ids)
			if err != nil {
				return err
			}
			want["modelPicker"], want["availableModels"] = str(string(picker)), str(string(allowed))
			for _, k := range keys {
				if err := set(k, want[k]); err != nil {
					return err
				}
			}

		} else {
			table := "model_providers." + singleDirectID(e.p.ID)
			if err := edit.SetTOMLTable(path, table, edit.KV{Path: "name", Value: e.p.Name}, edit.KV{Path: "base_url", Value: directBase(e.p, proto)}, edit.KV{Path: "wire_api", Value: "responses"}, edit.KV{Path: "experimental_bearer_token", Value: e.p.Key}); err != nil {
				return err
			}
			s.Last["@base"], s.Last["@token"], s.Last["@name"] = str(directBase(e.p, proto)), str(e.p.Key), str(e.p.Name)
			catalogPath := filepath.Join(filepath.Dir(path), "magpie-direct-models.json")
			catalogData := codexcat.CatalogAt(models, "")
			old, err := edit.Read(catalogPath)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			catalogData = codexcat.Keep(old, catalogData)
			if !bytes.Equal(old, catalogData) {
				if err := writeOwnList(catalogPath, catalogData); err != nil {
					return err
				}
			}
			for k, v := range map[string]*string{"model": str(current), "model_provider": str(singleDirectID(e.p.ID)), "model_catalog_json": str(at.native(catalogPath)), "openai_base_url": nil} {
				if err := set(k, v); err != nil {
					return err
				}
			}
		}
		save(s)
		return nil
	}
	// Keep the route, its saved originals and Codex's list together if a
	// write fails halfway through a binding or a restoration.
	paths := []string{path}
	if id == "codex" {
		paths = append(paths, filepath.Join(filepath.Dir(path), "magpie-direct-models.json"))
	}
	doAtomic := func(fn func() error) error {
		before := stashLoad()[key]
		err := edit.Atomically(fn, paths...)
		if err != nil {
			stash(map[string]string{key: before})
		}
		return err
	}
	syncDirect := a.Sync
	a.Sync = func() error { return doAtomic(syncDirect) }
	unwireDirect := a.Unwire
	a.Unwire = func() error { return doAtomic(unwireDirect) }
	if join := a.Join; join != nil {
		a.Join = func() (bool, error) {
			ok, err := join()
			if err == nil && ok && len(directEntries(id, a.Direct)) > 0 {
				err = a.Sync()
			}
			return ok, err
		}
	}
	// A bound agent's picker contains only models on that provider. Other model
	// settings cannot silently route the agent back through the gateway.
	for i := range a.Fields {
		f := &a.Fields[i]
		if f.Key != "login" && f.Key != "model" && f.Key != "subagent" && f.Key != "subagent_model" && f.Key != "memories" && !slices.Contains(claudeTiers, f.Key) && f.Key != "auto_mode" {
			continue
		}
		origGet, origSet, origOptions := f.Get, f.Set, f.Options
		field := f.Key
		f.Get = func() string {
			if s := load(); s != nil && field == "model" {
				return s.Provider + "/" + valueOf(read("model"))
			}
			return origGet()
		}
		f.Set = func(v string) error {
			if s := load(); s != nil {
				if field != "model" {
					return fmt.Errorf("%s is bound to one direct provider; change its model instead", a.Name)
				}
				entries := directEntries(id, a.Direct)
				if len(entries) == 0 {
					return fmt.Errorf("direct provider is unavailable")
				}
				m := strings.TrimPrefix(v, s.Provider+"/")
				models := entries[0].p.DirectModels(proto)
				if id == "claude" {
					var err error
					models, err = claudeDirectAllowed(models, s.Provider, s.Before["availableModels"])
					if err != nil {
						return err
					}
				}
				if !slices.ContainsFunc(models, func(x catalog.Model) bool { return x.ID == m }) {
					return fmt.Errorf("%s can only use models from %s", a.Name, entries[0].p.Name)
				}
				return doAtomic(func() error {
					if err := write("model", &m); err != nil {
						return err
					}
					s.Last["model"] = &m
					save(s)
					return a.Sync()
				})
			}
			if err := origSet(v); err != nil {
				return err
			}
			if field == "model" && len(directEntries(id, a.Direct)) > 0 && !a.Disconnected() && a.Wired() {
				return a.Sync()
			}
			return nil
		}
		f.Options = func(cur map[string]string) []Option {
			if s := load(); s != nil {
				if field != "model" {
					return nil
				}
				entries := directEntries(id, a.Direct)
				if len(entries) == 0 {
					return nil
				}
				var out []Option
				models := entries[0].p.DirectModels(proto)
				if id == "claude" {
					var err error
					models, err = claudeDirectAllowed(models, s.Provider, s.Before["availableModels"])
					if err != nil {
						return nil
					}
				}
				for _, m := range models {
					out = append(out, Option{Value: s.Provider + "/" + m.ID, Label: m.Name, Ref: s.Provider + "/" + m.ID, Group: entries[0].p.Name, Direct: entries[0].p.Name, Context: m.Context})
				}
				return out
			}
			if origOptions != nil {
				return origOptions(cur)
			}
			return nil
		}
	}
}

func valueOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// singleDirectID keeps a legacy provider id containing TOML punctuation in
// one table name. A dot otherwise denotes another table, not an id byte.
func singleDirectID(id string) string {
	if strings.HasPrefix(id, "_encoded_") {
		return provider.DirectID("_encoded_" + hex.EncodeToString([]byte(id)))
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return provider.DirectID("_encoded_" + hex.EncodeToString([]byte(id)))
		}
	}
	return provider.DirectID(id)
}

// claudeDirectAllowed keeps a user's existing model allowlist in force while
// translating catalog IDs to the provider's own IDs. Unrecognized entries do
// not broaden that list.
func claudeDirectAllowed(models []catalog.Model, providerID string, before *string) ([]catalog.Model, error) {
	if before == nil || *before == "null" {
		return models, nil
	}
	var allow []string
	if err := json.Unmarshal([]byte(*before), &allow); err != nil {
		return nil, fmt.Errorf("Claude Code's availableModels cannot be read: %w", err)
	}
	var out []catalog.Model
	for _, m := range models {
		matches := func(entry, id string) bool {
			return entry == id || strings.HasPrefix(id, entry+"-")
		}
		for _, entry := range allow {
			if entry == "" {
				continue
			}
			family := slices.Contains([]string{"opus", "sonnet", "haiku", "fable"}, entry) && (strings.HasPrefix(m.ID, "claude-"+entry+"-") || strings.Contains(m.ID, "/claude-"+entry+"-"))
			if family || matches(entry, m.ID) || matches(entry, providerID+"/"+m.ID) {
				out = append(out, m)
				break
			}
		}
	}
	return out, nil
}
