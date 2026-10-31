package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agent"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
)

// Pi takes the same fold: offered while it is connected, saved, written
// into its models.json, and taken out again when unticked.
func TestProviderDirectAgentsPi(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	dir := filepath.Join(home, "pi-agent")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(body string) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/save", strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	relay := func() providerJSON {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/providers", nil))
		var s providersJSON
		if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
			t.Fatalf("%v\n%s", err, w.Body.String())
		}
		for _, p := range s.Providers {
			if p.ID == "relay" {
				return p
			}
		}
		t.Fatal("relay not listed")
		return providerJSON{}
	}
	if code, out := post(`{"id":"relay","name":"Relay","key":"k-1","chat":"https://relay.example.test/v1","models":["m1"],"new":true}`); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if err := catalog.SaveLive("relay", "https://relay.example.test/v1", []catalog.Model{{ID: "m1", Context: 128000}}); err != nil {
		t.Fatal(err)
	}
	a, err := agent.Find("pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	info := relay()
	if !slices.ContainsFunc(info.Direct, func(d directAgentJSON) bool { return d.ID == "pi" && d.API == "chat" && d.Why == "" }) {
		t.Fatalf("offered %+v, want Pi on chat", info.Direct)
	}
	if code, out := post(`{"id":"relay","from":"relay","name":"Relay","chat":"https://relay.example.test/v1","models":["m1"],"directAgents":["pi"]}`); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	agent.SyncCatalogSettled()
	models := filepath.Join(dir, "models.json")
	b, err := os.ReadFile(models)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"magpie-direct-relay"`) {
		t.Fatalf("nothing written into Pi's models.json:\n%s", b)
	}
	if code, out := post(`{"id":"relay","from":"relay","name":"Relay","chat":"https://relay.example.test/v1","models":["m1"],"directAgents":[]}`); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	agent.SyncCatalogSettled()
	b, err = os.ReadFile(models)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"magpie-direct-relay"`) {
		t.Fatalf("unticked, still in Pi's models.json:\n%s", b)
	}
}

// A provider of the user's own is offered to the agents that can take it
// (OpenCode), saved with them, and written into that agent's own config; a
// save that leaves the tick out keeps it, and an agent or a provider that
// can't be handed over whole is refused. See
// docs/proposals/provider-direct.md.
func TestProviderDirectAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	mux := http.NewServeMux()
	providerRoutes(mux, nil)
	post := func(body string) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/provider/save", strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	get := func() providersJSON {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/providers", nil))
		var s providersJSON
		if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
			t.Fatalf("%v\n%s", err, w.Body.String())
		}
		return s
	}
	relay := func() providerJSON {
		for _, p := range get().Providers {
			if p.ID == "relay" {
				return p
			}
		}
		t.Fatal("relay not listed")
		return providerJSON{}
	}
	direct := func() []string {
		p, err := provider.Find("relay")
		if err != nil {
			t.Fatal(err)
		}
		return p.DirectAgents
	}

	if code, out := post(`{"id":"relay","name":"Relay","key":"k-1","chat":"https://relay.example.test/v1","models":["m1"],"new":true}`); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if err := catalog.SaveLive("relay", "https://relay.example.test/v1", []catalog.Model{{ID: "m1", Context: 128000}}); err != nil {
		t.Fatal(err)
	}
	oc, err := agent.Find("opencode")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(oc.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := oc.Connect(); err != nil {
		t.Fatal(err)
	}

	// the editor offers OpenCode, on chat
	info := relay()
	if !info.DirectOffered {
		t.Fatal("a provider of the user's offers no direct agents")
	}
	if info.DirectRefusal != "" {
		t.Fatalf("refused: %s", info.DirectRefusal)
	}
	if len(info.Direct) != 1 || info.Direct[0].ID != "opencode" || info.Direct[0].API != "chat" || info.Direct[0].Why != "" {
		t.Fatalf("offered %+v, want OpenCode on chat", info.Direct)
	}
	if info.DirectAgents != nil {
		t.Fatalf("offered as written into %v before a save", info.DirectAgents)
	}

	// ticked and saved: kept, and written into OpenCode's config
	if code, out := post(`{"id":"relay","from":"relay","name":"Relay","chat":"https://relay.example.test/v1","models":["m1"],"directAgents":["opencode"]}`); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if got := direct(); !slices.Equal(got, []string{"opencode"}) {
		t.Fatalf("saved %v, want [opencode]", got)
	}
	agent.SyncCatalogSettled()
	b, err := os.ReadFile(oc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"magpie-direct-relay"`) {
		t.Fatalf("nothing written into OpenCode's config:\n%s", b)
	}

	// a save that leaves the tick out keeps it, as a form without the fold
	if code, out := post(`{"id":"relay","from":"relay","name":"Relay","chat":"https://relay.example.test/v1","models":["m1"]}`); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if got := direct(); !slices.Equal(got, []string{"opencode"}) {
		t.Fatalf("a save that left it out saved %v", got)
	}

	// an agent that can't take a provider of its own is refused
	if code, out := post(`{"id":"relay","from":"relay","name":"Relay","chat":"https://relay.example.test/v1","models":["m1"],"directAgents":["codex"]}`); code == 200 {
		t.Fatalf("Codex accepted: %s", out)
	}
	if got := direct(); !slices.Equal(got, []string{"opencode"}) {
		t.Fatalf("a refused save changed it to %v", got)
	}

	// and so is a provider magpie can't hand over whole: one with a proxy
	// of its own
	if code, out := post(`{"id":"relay","from":"relay","name":"Relay","chat":"https://relay.example.test/v1","models":["m1"],"proxy":"direct","directAgents":["opencode"]}`); code == 200 {
		t.Fatalf("a proxied provider accepted: %s", out)
	}

	// unticked: taken out of the agent's config again
	if code, out := post(`{"id":"relay","from":"relay","name":"Relay","chat":"https://relay.example.test/v1","models":["m1"],"directAgents":[]}`); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if got := direct(); len(got) != 0 {
		t.Fatalf("unticked, saved %v", got)
	}
	agent.SyncCatalogSettled()
	b, err = os.ReadFile(oc.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"magpie-direct-relay"`) {
		t.Fatalf("unticked, still in OpenCode's config:\n%s", b)
	}
}
