package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

// A provider of the user's own can be written into Pi's models.json as one of
// its providers, so Pi asks it directly rather than through magpie
// (provider.Provider.DirectAgents). Unlike an OpenCode-shaped config, Pi's
// entry carries an api and baseUrl per model, so each of the provider's
// models goes on the API it is served on (Chat, Responses or Anthropic) and
// Pi speaks them all. See docs/proposals/provider-direct.md.

// piDirectProtos are the APIs Pi's models.json can ask a provider on, as its
// model api names them (openai-completions, openai-responses,
// anthropic-messages). Gemini's is left out: Pi's own agent folder is read by
// apps that run Pi's SDK on it and hand every request a fetch pi-ai's Google
// adapter refuses (piAsked, #3282), so a model served on Gemini alone stays
// in magpie's entry, where the gateway translates it.
var piDirectProtos = []provider.Protocol{provider.Chat, provider.Responses, provider.Anthropic}

// piDirect is Pi's half of the providers of the user's written into it: the
// capability (Direct), the entry's spelling in its model field, and a Sync
// that writes and removes the entries magpie owns.
func piDirect(a *Agent) {
	// the catalog id the provider names, which a WSL twin keeps ("pi") even
	// though its own id is pi@wsl:<distro>
	id := a.ID
	settings := a.Path
	models := filepath.Join(filepath.Dir(settings), "models.json")
	a.Direct = piDirectProtos
	spelled := a.Spelled
	a.Spelled = func(v string) bool { return directValue(v) || spelled != nil && spelled(v) }
	previousSync, previousUnwire, previousCheck := a.Sync, a.Unwire, a.Check
	a.Owned = func() bool { return piOwnsDirect(models) }
	a.Check = func() string {
		if p, _ := edit.GetJSON(settings, "defaultProvider"); provider.DirectOf(p) != "" {
			if err := provider.FileError(); err != nil {
				return err.Error()
			}
			if d := piDirectOff(a, p, models); d != "" {
				return d
			}
		}
		if previousCheck != nil {
			return previousCheck()
		}
		return ""
	}
	a.Unwire = func() error {
		if err := clearPiDirectProviders(models); err != nil {
			return err
		}
		if err := piScopeDirectWithout(settings, models); err != nil {
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
		if err := syncDirectPi(settings, models, id); err != nil {
			return err
		}
		if previousSync != nil {
			return previousSync()
		}
		return nil
	}
}

// piDirectEntry is one provider written into Pi's models.json: the provider,
// the API its entry defaults to, and its models each on the API it is served
// on.
type piDirectEntry struct {
	p      provider.Provider
	api    provider.Protocol
	models []provider.DirectModel
}

// piDirectEntries is the providers written into Pi (its DirectAgents names
// "pi"), in providers.json's order, each with the models it serves on an API
// Pi speaks itself.
func piDirectEntries(agentID string, protos []provider.Protocol) []piDirectEntry {
	if agentID != "pi" {
		return nil
	}
	var out []piDirectEntry
	for _, p := range provider.All() {
		if !p.DirectOffered() || !p.TakesDirectAgents() || !slices.Contains(p.DirectAgents, agentID) {
			continue
		}
		api := p.DirectAPI(protos)
		if api == "" {
			continue
		}
		models := p.DirectModelList(protos)
		if len(models) == 0 {
			continue
		}
		out = append(out, piDirectEntry{p: p, api: api, models: models})
	}
	return out
}

// piDirectIDs are the models written into Pi's own providers, as Pi's magpie
// entry names them (<provider>/<model>): those magpie's entry leaves out, so
// each is in Pi's picker once.
func piDirectIDs(agentID string) map[string]bool {
	out := map[string]bool{}
	for _, e := range piDirectEntries(agentID, piDirectProtos) {
		for _, dm := range e.models {
			out[e.p.ID+"/"+dm.Model.ID] = true
		}
	}
	return out
}

// syncDirectPi writes and removes the provider entries magpie owns in Pi's
// models.json: one per provider the user ticked Pi on, and none for one that
// was unticked, switched off or removed. An enabledModels entry left on a
// provider that is gone names nothing, so it goes with it.
func syncDirectPi(settings, models, agentID string) error {
	want := piDirectEntries(agentID, piDirectProtos)
	for _, e := range want {
		key := "providers." + provider.DirectID(e.p.ID)
		value := theirsKept(models, key, func() any { return piDirectJSON(e) }, "models")
		if _, ok := edit.GetJSON(models, key); !ok {
			if err := edit.SetJSON(models, edit.KV{Path: key, Value: value()}); err != nil {
				return err
			}
		} else if err := syncJSON(models, key, value); err != nil {
			return err
		}
	}
	raw, ok := edit.GetJSON(models, "providers")
	if !ok {
		return piScopeDirectWithout(settings, models)
	}
	for _, pair := range objectPairs([]byte(raw)) {
		pid := provider.DirectOf(pair.k)
		if pid == "" || slices.ContainsFunc(want, func(e piDirectEntry) bool { return e.p.ID == pid }) {
			continue
		}
		if err := edit.DelJSON(models, "providers."+pair.k); err != nil {
			return err
		}
	}
	return piScopeDirectWithout(settings, models)
}

// piDirectJSON is one provider of the user's as a Pi provider block: its
// name, the provider's own address and first key, and its models each with
// the API and address it is asked on, the window, reply limit, price and
// thinking levels Pi already gets for magpie's entries.
func piDirectJSON(e piDirectEntry) map[string]any {
	ms := make([]map[string]any, 0, len(e.models))
	for _, dm := range e.models {
		ms = append(ms, piDirectModelJSON(dm.Model, dm.API, directBase(e.p, dm.API)))
	}
	return map[string]any{
		"name":    e.p.Name,
		"baseUrl": directBase(e.p, e.api),
		"api":     piAPI(e.api),
		"apiKey":  e.p.Key,
		"models":  ms,
	}
}

// piDirectModelJSON is one model as a direct provider entry in Pi's
// models.json, on api at base. The model is written with the fields magpie's
// own entry gives it (piModelJSON), its api and baseUrl always its own: one
// provider's models need not all be asked the same way.
func piDirectModelJSON(m catalog.Model, api provider.Protocol, base string) map[string]any {
	e := piModelJSON(catalog.Model{
		ID: m.ID, Name: m.Name, APIs: []string{string(api)}, Efforts: m.Efforts,
		Reasoning: m.Reasoning, Images: m.Images, Context: m.Context, Output: m.Output, Price: m.Price,
	}, base, true)
	e["api"], e["baseUrl"] = piAPI(api), base
	return e
}

// piAPI is an API as Pi's models.json names it.
func piAPI(api provider.Protocol) string {
	switch api {
	case provider.Chat:
		return "openai-completions"
	case provider.Responses:
		return "openai-responses"
	case provider.Anthropic:
		return "anthropic-messages"
	}
	return ""
}

// piOwnsDirect reports whether Pi's models.json holds a provider magpie
// wrote for one of the user's (provider.DirectID).
func piOwnsDirect(models string) bool {
	raw, ok := edit.GetJSON(models, "providers")
	if !ok {
		return false
	}
	return slices.ContainsFunc(objectPairs([]byte(raw)), func(pair jsonPair) bool {
		return provider.DirectOf(pair.k) != ""
	})
}

// clearPiDirectProviders takes every provider entry magpie wrote for one of
// the user's out of Pi's models.json (Disconnect).
func clearPiDirectProviders(models string) error {
	raw, ok := edit.GetJSON(models, "providers")
	if !ok {
		return nil
	}
	for _, pair := range objectPairs([]byte(raw)) {
		if provider.DirectOf(pair.k) == "" {
			continue
		}
		if err := edit.DelJSON(models, "providers."+pair.k); err != nil {
			return err
		}
	}
	return nil
}

// piScopeDirectWithout takes the enabledModels entries on magpie-direct-…
// providers, or on models those providers no longer hold, out of Pi's list:
// an entry left on one that moved out would name nothing, and Pi says so
// every time it starts. The user's other entries, globs included, stay.
func piScopeDirectWithout(settings, models string) error {
	pats := piScope(settings)
	if pats == nil {
		return nil
	}
	have := map[string]map[string]bool{}
	if raw, ok := edit.GetJSON(models, "providers"); ok {
		for _, pair := range objectPairs([]byte(raw)) {
			if provider.DirectOf(pair.k) == "" {
				continue
			}
			var body struct {
				Models []struct {
					ID string `json:"id"`
				} `json:"models"`
			}
			raw, _ := pair.v.(json.RawMessage)
			if json.Unmarshal(raw, &body) != nil {
				have[pair.k] = nil // unknown: keep what names it
				continue
			}
			ids := map[string]bool{}
			for _, m := range body.Models {
				ids[m.ID] = true
			}
			have[pair.k] = ids
		}
	}
	kept := slices.DeleteFunc(slices.Clone(pats), func(p string) bool {
		if strings.ContainsAny(p, "*?[") {
			return false
		}
		if i := strings.LastIndex(p, ":"); i >= 0 && slices.Contains(piLevels, p[i+1:]) {
			p = p[:i]
		}
		pid, mid, ok := strings.Cut(strings.TrimSpace(p), "/")
		if !ok || provider.DirectOf(pid) == "" {
			return false
		}
		ids, known := have[pid]
		if !known {
			return true
		}
		return ids != nil && !ids[mid]
	})
	switch {
	case len(kept) == len(pats):
		return nil
	case len(kept) == 0:
		return edit.DelJSON(settings, "enabledModels")
	}
	return edit.SetJSON(settings, edit.KV{Path: "enabledModels", Value: kept})
}

// piDirectOff says what is off about the direct provider Pi starts on: the
// entry gone, its address changed, or its key no longer the provider's, as
// magpie wrote them. "" when it holds.
func piDirectOff(a *Agent, id, models string) string {
	raw, ok := edit.GetJSON(models, "providers."+id)
	if !ok {
		return a.Name + "'s provider " + id + " is gone from models.json, so the model it starts on names nothing"
	}
	p, err := provider.Find(provider.DirectOf(id))
	if err != nil {
		return a.Name + "'s provider " + id + " names a provider magpie no longer has"
	}
	api := p.DirectAPI(piDirectProtos)
	if api == "" {
		return a.Name + "'s provider " + id + " can no longer serve a model on an API Pi speaks"
	}
	var have struct {
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
	}
	if json.Unmarshal([]byte(raw), &have) != nil {
		return a.Name + "'s provider " + id + " can't be read: " + models
	}
	if want := directBase(*p, api); have.BaseURL != want {
		return fmt.Sprintf("%s's provider %s is at %s, not %s as magpie wrote it", a.Name, id, have.BaseURL, want)
	}
	if have.APIKey != p.Key {
		return fmt.Sprintf("%s's provider %s has another key, so its requests may be refused", a.Name, id)
	}
	return ""
}
