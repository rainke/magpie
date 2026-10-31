package agent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

// A provider of the user's own can be written into an agent's own config as
// that agent's provider, so the agent asks it directly rather than through
// magpie (provider.Provider.DirectAgents). The entry is magpie's, named
// provider.DirectID, and carries the provider's address, its first key and
// the models it serves on the API the agent speaks (provider.DirectModels).
// See docs/proposals/provider-direct.md.

// directEntry is one provider written into an agent's config: the provider,
// and the API its entry is written on.
type directEntry struct {
	p   provider.Provider
	api provider.Protocol
}

// directEntries is the providers written into the agent whose catalog id is
// catalogID (its DirectAgents names it), in providers.json's order, on the
// first of protos the agent speaks and the provider serves.
func directEntries(catalogID string, protos []provider.Protocol) []directEntry {
	var out []directEntry
	for _, p := range provider.All() {
		if !p.DirectOffered() || !p.TakesDirectAgents() || !slices.Contains(p.DirectAgents, catalogID) {
			continue
		}
		api := p.DirectAPI(protos)
		if api == "" || len(p.DirectModels(api)) == 0 {
			continue
		}
		out = append(out, directEntry{p: p, api: api})
	}
	return out
}

// directModels is the ids, as the agent's own list names them (the catalog's
// provider/model), of the models written into its direct entries: those it
// gets from the provider itself, which magpie's entry leaves out.
func directModels(catalogID string, protos []provider.Protocol) map[string]bool {
	out := map[string]bool{}
	for _, e := range directEntries(catalogID, protos) {
		for _, m := range e.p.DirectModels(e.api) {
			out[e.p.ID+"/"+m.ID] = true
		}
	}
	return out
}

// syncDirectOpenCode writes and removes the provider entries magpie owns in
// an OpenCode-shaped agent's config: one per provider the user ticked the
// agent on, and none for one that was unticked, switched off or removed.
func syncDirectOpenCode(path, catalogID string, protos []provider.Protocol) error {
	want := directEntries(catalogID, protos)
	for _, e := range want {
		key := "provider." + provider.DirectID(e.p.ID)
		value := openCodeEntryKept(path, key, func() any { return openCodeDirectJSON(e.p, e.api) })
		if _, ok := edit.GetJSON(path, key); !ok {
			if err := edit.SetJSON(path, edit.KV{Path: key, Value: value()}); err != nil {
				return err
			}
		} else if err := syncJSONInOrder(path, key, value); err != nil {
			return err
		}
	}
	// the entries magpie wrote before and shouldn't now: one unticked, a
	// provider switched off or removed
	raw, ok := edit.GetJSON(path, "provider")
	if !ok {
		return nil
	}
	for _, pair := range objectPairs([]byte(raw)) {
		pid := provider.DirectOf(pair.k)
		if pid == "" || slices.ContainsFunc(want, func(e directEntry) bool { return e.p.ID == pid }) {
			continue
		}
		if err := edit.DelJSON(path, "provider."+pair.k); err != nil {
			return err
		}
	}
	return nil
}

// clearDirectProviders takes every provider entry magpie wrote for one of
// the user's out of an OpenCode-shaped agent's config (Disconnect).
func clearDirectProviders(path string) error {
	raw, ok := edit.GetJSON(path, "provider")
	if !ok {
		return nil
	}
	for _, pair := range objectPairs([]byte(raw)) {
		if provider.DirectOf(pair.k) == "" {
			continue
		}
		if err := edit.DelJSON(path, "provider."+pair.k); err != nil {
			return err
		}
	}
	return nil
}

// openCodeDirectJSON is one provider of the user's as an OpenCode-shaped
// agent's provider entry: OpenCode's openai-compatible client, the
// provider's own chat address and first key, and the models it serves there
// with the fields magpie's own entry carries (openCodeModel).
func openCodeDirectJSON(p provider.Provider, api provider.Protocol) any {
	ms := map[string]any{}
	for _, m := range p.DirectModels(api) {
		ms[m.ID] = openCodeModel(m)
	}
	return map[string]any{
		"npm": "@ai-sdk/openai-compatible", "name": p.Name,
		"options": map[string]any{"baseURL": directBase(p, api), "apiKey": p.Key},
		"models":  ms,
	}
}

// directBase is the address an agent asks the provider at: the same base
// magpie sends its own request to, without the slash its client adds the
// endpoint to.
func directBase(p provider.Provider, api provider.Protocol) string {
	return strings.TrimRight(strings.TrimSpace(p.Base(api)), "/")
}

// openCodeOwnsDirect reports whether an OpenCode-shaped config at path holds a
// provider magpie wrote for one of the user's (provider.DirectID).
func openCodeOwnsDirect(path string) bool {
	raw, ok := edit.GetJSON(path, "provider")
	if !ok {
		return false
	}
	return slices.ContainsFunc(objectPairs([]byte(raw)), func(pair jsonPair) bool {
		return provider.DirectOf(pair.k) != ""
	})
}

// directValue reports whether v is a model an agent's config names in one of
// magpie's direct entries ("magpie-direct-<provider>/<model>"): magpie's
// wiring, whatever the model.
func directValue(v string) bool {
	pid, _, ok := strings.Cut(v, "/")
	return ok && provider.DirectOf(pid) != ""
}

// CheckDirectAgents says whether the agents p names can be written into as
// their own provider (provider.Provider.DirectAgents): each must be an agent
// magpie knows that can have a provider of its own, and one p can serve on an
// API it speaks. It is the editor's check before a Save; a providers.json
// from elsewhere keeps what it says, and a name that no longer applies is
// simply not written (directEntries).
func CheckDirectAgents(p provider.Provider) error {
	for _, id := range p.DirectAgents {
		if other := DirectBindingConflict(p.ID, id); other != "" {
			return fmt.Errorf("%s is already bound directly to %s; remove that binding first", id, other)
		}
		a, err := Find(id)
		if err != nil {
			return fmt.Errorf("%s isn't an agent magpie knows", id)
		}
		if len(a.Direct) == 0 {
			return fmt.Errorf("%s can't be given a provider of its own", a.Name)
		}
		if p.DirectAPI(a.Direct) == "" {
			if slices.ContainsFunc(p.Speaks(), func(pr provider.Protocol) bool { return slices.Contains(a.Direct, pr) }) {
				return fmt.Errorf("%s's first key is for %s alone, and %s speaks %s", p.Name,
					provider.ProtocolName(p.KeyProtocol), a.Name, provider.ProtocolNames(a.Direct))
			}
			return fmt.Errorf("%s speaks %s, and %s serves %s", a.Name,
				provider.ProtocolNames(a.Direct), p.Name, provider.ProtocolNames(p.Speaks()))
		}
	}
	return nil
}

// DirectBindingConflict names the other provider bound to a single-upstream agent.
func DirectBindingConflict(providerID, agentID string) string {
	if agentID != "claude" && agentID != "codex" {
		return ""
	}
	for _, other := range provider.All() {
		if other.ID != providerID && slices.Contains(other.DirectAgents, agentID) {
			return other.Name
		}
	}
	return ""
}
