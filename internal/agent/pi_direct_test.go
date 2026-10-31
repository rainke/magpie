package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

// piDirectSetup writes a Pi install in the test's home, on one of magpie's
// models, with a provider of the user's own that serves a model on each API
// Pi can ask, one that Pi can't (Gemini's), and a provider Pi had already.
func piDirectSetup(t *testing.T) (home, modelsPath string) {
	t.Helper()
	home = syncHome(t)
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "models.json"), `{"providers":{"ollama":{
	  "name":"Ollama","baseUrl":"http://127.0.0.1:11434/v1","api":"openai-completions","apiKey":"local",
	  "models":[{"id":"qwen"}]}}}`)
	if err := provider.Save(provider.Provider{
		ID: "relay", Name: "Relay", Key: "k-1", Keys: []provider.KeyAccount{{Name: "second", Key: "k-2"}},
		Chat: "https://relay.example.test/v1", Responses: "https://relay.example.test/v1", Anthropic: "https://relay.example.test",
		Gemini: "https://relay.example.test/v1beta",
		Models: []string{"chat-1", "claude-1", "gpt-1", "gem-1"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("relay", "https://relay.example.test/v1", []catalog.Model{
		{ID: "chat-1", APIs: []string{string(provider.Chat)}, Context: 128000, Output: 32000, Efforts: []string{"low", "high"},
			Price: &catalog.Price{Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 1}},
		{ID: "claude-1", APIs: []string{string(provider.Anthropic)}},
		{ID: "gpt-1", APIs: []string{string(provider.Responses)}},
		{ID: "gem-1", APIs: []string{string(provider.Gemini)}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := pi(home).Connect(); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(dir, "models.json")
}

type piEntryJSON struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	API      string         `json:"api"`
	BaseURL  string         `json:"baseUrl"`
	Context  int            `json:"contextWindow"`
	Max      int            `json:"maxTokens"`
	Cost     map[string]any `json:"cost"`
	Reason   bool           `json:"reasoning"`
	Thinking map[string]any `json:"thinkingLevelMap"`
}

type piProviderJSON struct {
	Name    string        `json:"name"`
	BaseURL string        `json:"baseUrl"`
	API     string        `json:"api"`
	APIKey  string        `json:"apiKey"`
	Models  []piEntryJSON `json:"models"`
}

type piModelsJSON struct {
	Providers map[string]piProviderJSON `json:"providers"`
}

func readPiModels(t *testing.T, path string) piModelsJSON {
	t.Helper()
	var m piModelsJSON
	if err := json.Unmarshal([]byte(readFile(path)), &m); err != nil {
		t.Fatalf("%v\n%s", err, readFile(path))
	}
	return m
}

func piModelIn(p piProviderJSON, id string) (int, bool) {
	for i, m := range p.Models {
		if m.ID == id {
			return i, true
		}
	}
	return 0, false
}

func mustPiModel(t *testing.T, p piProviderJSON, id string) int {
	t.Helper()
	i, ok := piModelIn(p, id)
	if !ok {
		t.Fatalf("%s missing: %+v", id, p.Models)
	}
	return i
}

// A provider of the user's ticked for Pi is written into its models.json as
// Pi's own provider: the provider's address and first key alone, and each
// model on the API it is served on, with the window, reply limit and price
// magpie gives Pi. Magpie's entry leaves those models out; the model served
// on Gemini alone stays there, where the gateway translates it.
func TestPiDirectWritesTheProvider(t *testing.T) {
	home, path := piDirectSetup(t)
	tick(t, "relay", "pi")
	a := pi(home)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	cfg := readPiModels(t, path)
	if _, ok := cfg.Providers["ollama"]; !ok {
		t.Fatal("the user's own provider went")
	}
	d, ok := cfg.Providers["magpie-direct-relay"]
	if !ok {
		t.Fatalf("no magpie-direct-relay:\n%s", readFile(path))
	}
	if d.Name != "Relay" || d.BaseURL != "https://relay.example.test/v1" || d.API != "openai-completions" {
		t.Errorf("entry: %+v", d)
	}
	if d.APIKey != "k-1" {
		t.Errorf("apiKey %q, want the first key", d.APIKey)
	}
	if len(d.Models) != 3 {
		t.Fatalf("models: %+v", d.Models)
	}
	chat := d.Models[mustPiModel(t, d, "chat-1")]
	if chat.API != "openai-completions" || chat.BaseURL != "https://relay.example.test/v1" || chat.Name == "" ||
		chat.Context != 128000 || chat.Max != 32000 || chat.Cost["input"] != float64(1) || chat.Cost["output"] != float64(2) {
		t.Errorf("chat-1: %+v", chat)
	}
	if !chat.Reason || chat.Thinking["low"] != "low" || chat.Thinking["high"] != "high" {
		t.Errorf("chat-1's thinking levels: %+v", chat.Thinking)
	}
	claude := d.Models[mustPiModel(t, d, "claude-1")]
	if claude.API != "anthropic-messages" || claude.BaseURL != "https://relay.example.test" {
		t.Errorf("claude-1: %+v", claude)
	}
	gpt := d.Models[mustPiModel(t, d, "gpt-1")]
	if gpt.API != "openai-responses" || gpt.BaseURL != "https://relay.example.test/v1" {
		t.Errorf("gpt-1: %+v", gpt)
	}
	if _, ok := piModelIn(d, "gem-1"); ok {
		t.Error("a Gemini-only model written on a direct entry")
	}
	m := cfg.Providers["magpie"]
	for _, id := range []string{"relay/chat-1", "relay/claude-1", "relay/gpt-1"} {
		if _, ok := piModelIn(m, id); ok {
			t.Errorf("%s is in magpie's entry too", id)
		}
	}
	if _, ok := piModelIn(m, "relay/gem-1"); !ok {
		t.Fatal("a Gemini-only model left magpie's entry")
	}
	before := readFile(path)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if after := readFile(path); after != before {
		t.Fatalf("a second Sync wrote:\n%s\n---\n%s", before, after)
	}
}

// Unticking Pi or switching the provider off takes the entry out and puts
// the models back in magpie's, where the gateway serves them again.
func TestPiDirectUntickedAndOff(t *testing.T) {
	home, path := piDirectSetup(t)
	tick(t, "relay", "pi")
	a := pi(home)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPiModels(t, path).Providers["magpie-direct-relay"]; !ok {
		t.Fatal("nothing written")
	}
	tick(t, "relay")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	cfg := readPiModels(t, path)
	if _, ok := cfg.Providers["magpie-direct-relay"]; ok {
		t.Fatal("unticked, the entry stayed")
	}
	if _, ok := piModelIn(cfg.Providers["magpie"], "relay/chat-1"); !ok {
		t.Fatal("its model did not go back to magpie's entry")
	}

	tick(t, "relay", "pi")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	p, err := provider.Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	p.Off = true
	if err := provider.Save(*p); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, ok := readPiModels(t, path).Providers["magpie-direct-relay"]; ok {
		t.Fatal("a switched-off provider's entry stayed")
	}
}

// Disconnect takes magpie's direct entry out and leaves the user's own
// provider and its model in place.
func TestPiDirectDisconnectKeepsTheUsersEntry(t *testing.T) {
	home, path := piDirectSetup(t)
	tick(t, "relay", "pi")
	a := pi(home)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	cfg := readPiModels(t, path)
	if _, ok := cfg.Providers["magpie-direct-relay"]; ok {
		t.Fatal("Disconnect left the entry")
	}
	if _, ok := cfg.Providers["magpie"]; ok {
		t.Fatal("Disconnect left magpie's provider")
	}
	if _, ok := cfg.Providers["ollama"]; !ok {
		t.Fatal("Disconnect took the user's own provider")
	}
}

// Pi on a direct provider counts as magpie's wiring: its model field says
// so, Disconnect is offered, and a key changed by hand under it is reported
// rather than silently taken for magpie's.
func TestPiDirectIsWiredAndDriftSeesAChangedKey(t *testing.T) {
	home, path := piDirectSetup(t)
	tick(t, "relay", "pi")
	a := pi(home)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := a.Field("model").Set("magpie-direct-relay/chat-1"); err != nil {
		t.Fatal(err)
	}
	if got := a.Field("model").Get(); got != "magpie-direct-relay/chat-1" {
		t.Fatalf("model reads %q", got)
	}
	if !a.Wired() {
		t.Fatal("Pi on a direct entry is not wired")
	}
	if !a.Owned() {
		t.Fatal("Pi with a direct entry doesn't say it holds one")
	}
	if err := edit.SetJSON(path, edit.KV{Path: "providers.magpie-direct-relay.apiKey", Value: "someone else's"}); err != nil {
		t.Fatal(err)
	}
	if got := a.Check(); !strings.Contains(got, "another key") {
		t.Fatalf("Check = %q", got)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if got, _ := edit.GetJSON(path, "providers.magpie-direct-relay.apiKey"); got != "k-1" {
		t.Fatalf("the key was not put back: %q", got)
	}
}

// A direct provider's model picked into enabledModels leaves it once the
// provider goes: the entry would name nothing, and Pi says so at every start.
func TestPiDirectScopeFollowsTheEntry(t *testing.T) {
	home, path := piDirectSetup(t)
	settings := filepath.Join(filepath.Dir(path), "settings.json")
	if err := edit.SetJSON(settings, edit.KV{Path: "enabledModels", Value: []string{"magpie/relay/gpt-1", "magpie-direct-relay/chat-1", "ollama/qwen"}}); err != nil {
		t.Fatal(err)
	}
	tick(t, "relay", "pi")
	a := pi(home)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(settings); !strings.Contains(got, "magpie-direct-relay/chat-1") {
		t.Fatalf("a live direct entry's scope went: %s", got)
	}
	tick(t, "relay")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(settings); strings.Contains(got, "magpie-direct-relay/chat-1") {
		t.Fatalf("a gone direct entry stayed in enabledModels: %s", got)
	}
	if got := readFile(settings); !strings.Contains(got, "ollama/qwen") {
		t.Fatalf("the user's own scope went: %s", got)
	}
}

// A provider magpie can't hand over whole is not written; its models stay
// in magpie's entry, where the gateway serves them.
func TestPiDirectRefusedProvider(t *testing.T) {
	home, path := piDirectSetup(t)
	if err := provider.Save(provider.Provider{ID: "proxied", Name: "Proxied", Key: "k", Chat: "https://proxied.example.test/v1",
		Proxy: "http://127.0.0.1:9", Models: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("proxied", "https://proxied.example.test/v1", []catalog.Model{{ID: "m"}}); err != nil {
		t.Fatal(err)
	}
	tick(t, "proxied", "pi")
	a := pi(home)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	cfg := readPiModels(t, path)
	if _, ok := cfg.Providers["magpie-direct-proxied"]; ok {
		t.Fatal("a provider with a proxy of its own was written")
	}
	if _, ok := piModelIn(cfg.Providers["magpie"], "proxied/m"); !ok {
		t.Fatal("its model left magpie's entry")
	}
}

// A WSL distro's Pi gets the provider written at its own models.json with
// the same address and key, and this machine's Pi is left alone.
func TestPiDirectWSL(t *testing.T) {
	root, home := piDistroHome(t, "")
	if err := provider.Save(provider.Provider{ID: "relay", Name: "Relay", Key: "k-1",
		Chat: "https://relay.example.test/v1", Models: []string{"chat-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("relay", "https://relay.example.test/v1", []catalog.Model{{ID: "chat-1"}}); err != nil {
		t.Fatal(err)
	}
	d := distro{Name: "Ubuntu", Root: root, Home: "/home/me", Has: map[string]bool{"dir:.pi": true}, Running: true}
	a := wslPi(d)
	if err := a.Field("model").Set("magpie/relay/chat-1"); err != nil {
		t.Fatal(err)
	}
	tick(t, "relay", "pi")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	models := filepath.Join(home, ".pi", "agent", "models.json")
	cfg := readPiModels(t, models)
	e, ok := cfg.Providers["magpie-direct-relay"]
	if !ok {
		t.Fatalf("nothing written in the distro:\n%s", readFile(models))
	}
	if e.APIKey != "k-1" || e.BaseURL != "https://relay.example.test/v1" {
		t.Errorf("distro entry: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".pi", "agent", "models.json")); err == nil {
		t.Fatal("this machine's Pi was written")
	}
}

// The provider's first key's protocol is kept: a model on another API stays
// in magpie's entry, where the gateway may use another key for it.
func TestPiDirectKeyProtocol(t *testing.T) {
	home, path := piDirectSetup(t)
	p, err := provider.Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	p.KeyProtocol = provider.Anthropic
	if err := provider.Save(*p); err != nil {
		t.Fatal(err)
	}
	tick(t, "relay", "pi")
	a := pi(home)
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	cfg := readPiModels(t, path)
	d := cfg.Providers["magpie-direct-relay"]
	if len(d.Models) != 1 || d.Models[0].ID != "claude-1" || d.Models[0].API != "anthropic-messages" {
		t.Fatalf("models: %+v", d.Models)
	}
	if _, ok := piModelIn(cfg.Providers["magpie"], "relay/chat-1"); !ok {
		t.Fatal("a model off the first key's API left magpie's entry")
	}
}

// Each of Pi's models is named once, by the provider that serves it.
func TestPiDirectLeavesNoModelTwice(t *testing.T) {
	home, path := piDirectSetup(t)
	tick(t, "relay", "pi")
	if err := pi(home).Sync(); err != nil {
		t.Fatal(err)
	}
	cfg := readPiModels(t, path)
	seen := map[string]int{}
	for id, p := range cfg.Providers {
		for _, m := range p.Models {
			seen[m.ID]++
			if strings.HasPrefix(id, "magpie-direct-") && strings.Contains(m.ID, "/") {
				t.Errorf("%s's model %s is named as a provider and a model", id, m.ID)
			}
		}
	}
	for _, id := range []string{"chat-1", "claude-1", "gpt-1", "relay/gem-1"} {
		if seen[id] != 1 {
			t.Errorf("%s appears %d times", id, seen[id])
		}
	}
}
