package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

// directConfig is an OpenCode-shaped agent's config as a test reads it.
type directConfig struct {
	Theme    string `json:"theme"`
	Model    string `json:"model"`
	Provider map[string]struct {
		NPM     string `json:"npm"`
		Name    string `json:"name"`
		Options struct {
			BaseURL string `json:"baseURL"`
			APIKey  string `json:"apiKey"`
		} `json:"options"`
		Models map[string]struct {
			Name  string         `json:"name"`
			Limit *directLimits  `json:"limit"`
			Cost  map[string]any `json:"cost"`
		} `json:"models"`
	} `json:"provider"`
}

type directLimits struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

// directSetup writes an OpenCode install in the test's home, with the
// providers a direct test writes into it: one the user added with a chat
// address and three keys, and one that serves a model on Messages alone.
func directSetup(t *testing.T) (path string, home string) {
	t.Helper()
	home = os.Getenv("HOME")
	if err := provider.Save(provider.Provider{
		ID: "relay", Name: "Relay", Key: "k-1", Keys: []provider.KeyAccount{{Name: "second", Key: "k-2"}, {Name: "third", Key: "k-3"}},
		Chat: "https://relay.example.test/v1", Anthropic: "https://relay.example.test",
		Models: []string{"m1", "claude"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{
		ID: "anth", Name: "Anth", Key: "a-1", Anthropic: "https://anth.example.test",
		Models: []string{"claude"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("relay", "https://relay.example.test/v1", []catalog.Model{
		{ID: "m1", Context: 128000, Price: &catalog.Price{Input: 1, Output: 2}},
		{ID: "claude", APIs: []string{string(provider.Anthropic)}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("anth", "https://anth.example.test", []catalog.Model{
		{ID: "claude", APIs: []string{string(provider.Anthropic)}},
	}); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(home, ".config", "opencode", "opencode.json")
	writeFile(t, path, `{"theme":"dark","provider":{"ollama":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://127.0.0.1:11434/v1"}}}}`)
	// the user connected OpenCode to magpie: it is on one of magpie's
	// models, and the config is magpie's to keep in step
	oc := opencode(home, filepath.Join(home, ".config"))
	if err := oc.Connect(); err != nil {
		t.Fatal(err)
	}
	return path, home
}

// tick is the editor's tick: the provider is written into the agent named.
func tick(t *testing.T, id string, agents ...string) {
	t.Helper()
	p, err := provider.Find(id)
	if err != nil {
		t.Fatal(err)
	}
	p.DirectAgents = agents
	if err := provider.Save(*p); err != nil {
		t.Fatal(err)
	}
}

func readDirect(t *testing.T, path string) directConfig {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg directConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return cfg
}

// A provider of the user's ticked for OpenCode is written into OpenCode's
// own config: its chat address, its first key alone, and the models it
// serves there, which magpie's own entry then leaves out. The user's own
// providers and their keys are untouched, and a second Sync writes nothing.
func TestOpenCodeDirectWritesTheProvider(t *testing.T) {
	path, home := directSetup(t)
	tick(t, "relay", "opencode")
	oc := opencode(home, filepath.Join(home, ".config"))
	if !oc.Wired() {
		t.Fatal("OpenCode on one of magpie's models is not wired")
	}
	if err := oc.Sync(); err != nil {
		t.Fatal(err)
	}
	cfg := readDirect(t, path)
	if cfg.Theme != "dark" || cfg.Provider["ollama"].NPM == "" {
		t.Fatalf("the user's own settings went: %+v", cfg)
	}
	d, ok := cfg.Provider["magpie-direct-relay"]
	if !ok {
		t.Fatalf("no magpie-direct-relay: %+v", cfg.Provider)
	}
	if d.NPM != "@ai-sdk/openai-compatible" || d.Name != "Relay" {
		t.Errorf("entry: %+v", d)
	}
	if d.Options.BaseURL != "https://relay.example.test/v1" {
		t.Errorf("baseURL %q, want the provider's own chat address", d.Options.BaseURL)
	}
	// the first key alone, never the gateway's and never the others
	if d.Options.APIKey != "k-1" {
		t.Errorf("apiKey %q, want the first key", d.Options.APIKey)
	}
	m, ok := d.Models["m1"]
	if !ok {
		t.Fatalf("m1 missing: %+v", d.Models)
	}
	if m.Name == "" || m.Limit == nil || m.Limit.Context != 128000 {
		t.Errorf("m1: %+v", m)
	}
	if m.Cost["input"] != float64(1) || m.Cost["output"] != float64(2) {
		t.Errorf("m1 cost: %v", m.Cost)
	}
	// the model it serves on Messages alone is not on a chat entry
	if _, ok := d.Models["claude"]; ok {
		t.Error("a Messages-only model written on the chat entry")
	}
	// and magpie's own entry leaves the direct model out, once
	if _, ok := cfg.Provider["magpie"].Models["relay/m1"]; ok {
		t.Error("relay/m1 is in magpie's entry too")
	}
	if _, ok := cfg.Provider["magpie"].Models["relay/claude"]; !ok {
		t.Error("relay/claude left magpie's entry, where the gateway translates it")
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := oc.Sync(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("a second Sync wrote:\n%s\n---\n%s", before, after)
	}
}

// A provider whose APIs and OpenCode's don't meet is not written at all,
// and its models stay in magpie's entry.
func TestOpenCodeDirectNeedsAnAPIInCommon(t *testing.T) {
	path, home := directSetup(t)
	tick(t, "anth", "opencode")
	oc := opencode(home, filepath.Join(home, ".config"))
	if err := oc.Sync(); err != nil {
		t.Fatal(err)
	}
	cfg := readDirect(t, path)
	if _, ok := cfg.Provider["magpie-direct-anth"]; ok {
		t.Error("a Messages-only provider written into OpenCode")
	}
	if _, ok := cfg.Provider["magpie"].Models["anth/claude"]; !ok {
		t.Error("its model left magpie's entry")
	}
}

// Unticking the agent takes the entry out and leaves everything else, and so
// does Disconnect.
func TestOpenCodeDirectUntickedAndDisconnected(t *testing.T) {
	path, home := directSetup(t)
	tick(t, "relay", "opencode")
	oc := opencode(home, filepath.Join(home, ".config"))
	if err := oc.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, ok := readDirect(t, path).Provider["magpie-direct-relay"]; !ok {
		t.Fatal("nothing written")
	}
	tick(t, "relay")
	if err := oc.Sync(); err != nil {
		t.Fatal(err)
	}
	cfg := readDirect(t, path)
	if _, ok := cfg.Provider["magpie-direct-relay"]; ok {
		t.Fatal("unticked, the entry stayed")
	}
	if _, ok := cfg.Provider["ollama"]; !ok {
		t.Fatal("the user's own provider went with it")
	}

	// Disconnect is the other way out: a provider left ticked is still
	// magpie's entry in the agent's config
	tick(t, "relay", "opencode")
	if err := oc.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, ok := readDirect(t, path).Provider["magpie-direct-relay"]; !ok {
		t.Fatal("nothing written again")
	}
	if err := oc.Disconnect(); err != nil {
		t.Fatal(err)
	}
	cfg = readDirect(t, path)
	if _, ok := cfg.Provider["magpie-direct-relay"]; ok {
		t.Fatal("Disconnect left the entry")
	}
	if _, ok := cfg.Provider["ollama"]; !ok {
		t.Fatal("Disconnect took the user's own provider")
	}
	if cfg.Model != "" {
		t.Errorf("model %q left after Disconnect", cfg.Model)
	}
}

// The direct entry is magpie's for Wired and for a model picked in OpenCode's
// own picker: an agent on one is connected, and Disconnect is offered.
func TestOpenCodeDirectPickIsMagpieWired(t *testing.T) {
	for _, v := range []string{"magpie-direct-relay/m1", "magpie-direct-relay/claude"} {
		if !directValue(v) {
			t.Errorf("%s not read as magpie's", v)
		}
	}
	for _, v := range []string{"relay/m1", "magpie-direct-/m1", "ollama/m1"} {
		if directValue(v) {
			t.Errorf("%s read as a direct entry", v)
		}
	}
	path, home := directSetup(t)
	oc := opencode(home, filepath.Join(home, ".config"))
	if err := edit.SetJSON(path, edit.KV{Path: "model", Value: "magpie-direct-relay/m1"}); err != nil {
		t.Fatal(err)
	}
	if !oc.Wired() {
		t.Fatal("OpenCode on a direct entry is not wired")
	}
	if got := oc.Values()["model"]; got != "magpie-direct-relay/m1" {
		t.Errorf("model read as %q", got)
	}
	if !slices.Contains(oc.Direct, provider.Chat) {
		t.Error("OpenCode doesn't say it can take a chat entry")
	}
}

// A provider written into OpenCode stays magpie's to keep in step even once
// the agent is on a model of its own: the next catalog round still takes an
// unticked entry out.
func TestOpenCodeDirectKeptInStepOffMagpie(t *testing.T) {
	path, home := directSetup(t)
	tick(t, "relay", "opencode")
	oc := opencode(home, filepath.Join(home, ".config"))
	if err := oc.Sync(); err != nil {
		t.Fatal(err)
	}
	if !openCodeOwnsDirect(path) {
		t.Fatal("nothing written")
	}
	// the user picks one of their own providers in OpenCode's picker
	if err := edit.SetJSON(path, edit.KV{Path: "model", Value: "ollama/qwen"}); err != nil {
		t.Fatal(err)
	}
	if oc.Wired() {
		t.Fatal("an agent on its own model counts as wired")
	}
	tick(t, "relay")
	SyncCatalog()
	if openCodeOwnsDirect(path) {
		t.Fatal("an unticked entry stayed while the agent was on its own model")
	}
}
