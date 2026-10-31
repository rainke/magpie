package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

func TestSingleDirectBinding(t *testing.T) {
	for _, id := range []string{"claude", "codex"} {
		t.Run(id, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("CODEX_HOME", "")
			p := provider.Provider{ID: "relay", Name: "Relay", Key: "secret", Anthropic: "https://relay.example.test", Responses: "https://relay.example.test/v1", Models: []string{"first", "second"}}
			if err := provider.Save(p); err != nil {
				t.Fatal(err)
			}
			var a *Agent
			if id == "claude" {
				a = claude(home)
			} else {
				a = codex(home)
			}
			if err := os.MkdirAll(filepath.Dir(a.Path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := a.Connect(); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(a.Path)
			if err != nil {
				t.Fatal(err)
			}
			tick(t, "relay", id)
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			if !a.Wired() {
				t.Fatal("direct binding is not connected")
			}
			a.LastUsed = func() time.Time { return time.Now().Add(-time.Minute) }
			a.reach = func() string { return "http://127.0.0.1:1" }
			if drift := a.Drift(); drift != nil {
				t.Fatalf("direct request reported as gateway drift: %+v", drift)
			}
			if id == "claude" {
				base, _ := edit.GetJSON(a.Path, "env.ANTHROPIC_BASE_URL")
				token, _ := edit.GetJSON(a.Path, "env.ANTHROPIC_API_KEY")
				if base != p.Anthropic || token != p.Key {
					t.Fatalf("Claude route %q %q", base, token)
				}
			} else {
				selected, _ := edit.GetTOMLTop(a.Path, "model_provider")
				table, _ := edit.GetTOMLTable(a.Path, "model_providers.magpie-direct-relay")
				if selected != "magpie-direct-relay" || table["base_url"] != p.Responses || table["experimental_bearer_token"] != p.Key {
					t.Fatalf("Codex route %q %+v", selected, table)
				}
			}
			if id == "codex" {
				list := filepath.Join(filepath.Dir(a.Path), "magpie-direct-models.json")
				if err := edit.SetJSON(list, edit.KV{Path: "models.0.custom_user_setting", Value: "keep me"}); err != nil {
					t.Fatal(err)
				}
				if err := a.Sync(); err != nil {
					t.Fatal(err)
				}
				if v, _ := edit.GetJSON(list, "models.0.custom_user_setting"); v != "keep me" {
					t.Fatalf("user's model setting lost: %q", v)
				}
			}
			firstConfig, err := os.ReadFile(a.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			again, err := os.ReadFile(a.Path)
			if err != nil || !bytes.Equal(firstConfig, again) {
				t.Fatal("a second sync changed the config", err)
			}
			updated, err := provider.Find("relay")
			if err != nil {
				t.Fatal(err)
			}
			updated.Key = "rotated"
			if err := provider.Save(*updated); err != nil {
				t.Fatal(err)
			}
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			if id == "claude" {
				v, _ := edit.GetJSON(a.Path, "env.ANTHROPIC_API_KEY")
				if v != "rotated" {
					t.Fatalf("rotated token %q", v)
				}
			} else {
				table, _ := edit.GetTOMLTable(a.Path, "model_providers.magpie-direct-relay")
				if table["experimental_bearer_token"] != "rotated" {
					t.Fatalf("rotated token %q", table["experimental_bearer_token"])
				}
			}
			if err := a.Pick("model", "another/model"); err == nil {
				t.Fatal("allowed a model from another provider")
			}
			if err := a.Pick("model", "relay/second"); err != nil {
				t.Fatal(err)
			}
			var got string
			if id == "claude" {
				got, _ = edit.GetJSON(a.Path, "model")
			} else {
				got, _ = edit.GetTOMLTop(a.Path, "model")
			}
			if got != "second" {
				t.Fatalf("agent model %q", got)
			}
			other := provider.Provider{ID: "other", Name: "Other", Key: "key", Anthropic: p.Anthropic, Responses: p.Responses, Models: []string{"x"}, DirectAgents: []string{id}}
			if err := CheckDirectAgents(other); err == nil || !strings.Contains(err.Error(), "already bound") {
				t.Fatalf("second binding: %v", err)
			}
			tick(t, "relay")
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			if id == "claude" {
				got, _ = edit.GetJSON(a.Path, "env.ANTHROPIC_BASE_URL")
			} else {
				got, _ = edit.GetTOMLTop(a.Path, "model_provider")
			}
			if got == p.Anthropic || got == "magpie-direct-relay" {
				t.Fatalf("direct route left after untick: %q", got)
			}
			beforePath := filepath.Join(home, "before")
			if err := os.WriteFile(beforePath, original, 0600); err != nil {
				t.Fatal(err)
			}
			var beforeModel, afterModel string
			if id == "claude" {
				beforeModel, _ = edit.GetJSON(beforePath, "model")
				afterModel, _ = edit.GetJSON(a.Path, "model")
			} else {
				beforeModel, _ = edit.GetTOMLTop(beforePath, "model")
				afterModel, _ = edit.GetTOMLTop(a.Path, "model")
			}
			if beforeModel != afterModel {
				t.Fatalf("restored model %q, want %q", afterModel, beforeModel)
			}
		})
	}
}

// A hand-edited endpoint must never receive the token displaced by binding.
func TestSingleDirectHandEditAndDisconnect(t *testing.T) {
	for _, id := range []string{"claude", "codex"} {
		t.Run(id, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("CODEX_HOME", "")
			p := provider.Provider{ID: "relay", Name: "Relay", Key: "secret", Anthropic: "https://relay.example.test", Responses: "https://relay.example.test/v1", Models: []string{"one"}}
			if err := provider.Save(p); err != nil {
				t.Fatal(err)
			}
			var a *Agent
			if id == "claude" {
				a = claude(home)
			} else {
				a = codex(home)
			}
			if err := os.MkdirAll(filepath.Dir(a.Path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := a.Connect(); err != nil {
				t.Fatal(err)
			}
			tick(t, "relay", id)
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			if id == "claude" {
				if err := edit.SetJSON(a.Path, edit.KV{Path: "env.ANTHROPIC_BASE_URL", Value: "https://user.example.test"}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := edit.SetTOMLTop(a.Path, edit.KV{Path: "model_provider", Value: "user-provider"}); err != nil {
					t.Fatal(err)
				}
			}
			if a.Check() == "" {
				t.Fatal("manual route edit was not reported")
			}
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			tick(t, "relay")
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			if id == "claude" {
				base, _ := edit.GetJSON(a.Path, "env.ANTHROPIC_BASE_URL")
				token, has := edit.GetJSON(a.Path, "env.ANTHROPIC_API_KEY")
				if base != "https://user.example.test" || has {
					t.Fatalf("manual endpoint %q token present %v %q", base, has, token)
				}
			} else {
				selected, _ := edit.GetTOMLTop(a.Path, "model_provider")
				if selected != "user-provider" {
					t.Fatalf("manual provider replaced: %q", selected)
				}
			}
		})
	}
}

func TestSingleDirectReconnect(t *testing.T) {
	for _, id := range []string{"claude", "codex"} {
		t.Run(id, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("CODEX_HOME", "")
			p := provider.Provider{ID: "relay", Name: "Relay", Key: "secret", Anthropic: "https://relay.example.test", Responses: "https://relay.example.test/v1", Models: []string{"one"}}
			if err := provider.Save(p); err != nil {
				t.Fatal(err)
			}
			var a *Agent
			if id == "claude" {
				a = claude(home)
			} else {
				a = codex(home)
			}
			if err := os.MkdirAll(filepath.Dir(a.Path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := a.Connect(); err != nil {
				t.Fatal(err)
			}
			tick(t, "relay", id)
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			if err := a.Disconnect(); err != nil {
				t.Fatal(err)
			}
			if a.Wired() {
				t.Fatal("still connected after disconnect")
			}
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			if a.Wired() {
				t.Fatal("sync reconnected a disconnected agent")
			}
			if err := a.Connect(); err != nil {
				t.Fatal(err)
			}
			if id == "claude" {
				base, _ := edit.GetJSON(a.Path, "env.ANTHROPIC_BASE_URL")
				if base != p.Anthropic {
					t.Fatalf("reconnected endpoint: %q", base)
				}
			} else {
				selected, _ := edit.GetTOMLTop(a.Path, "model_provider")
				if selected != "magpie-direct-relay" {
					t.Fatalf("reconnected provider: %q", selected)
				}
			}
		})
	}
}

func TestSingleDirectIDForLegacyProvider(t *testing.T) {
	id := singleDirectID("legacy.provider")
	if strings.Contains(id, ".") {
		t.Fatalf("TOML table id %q contains a dot", id)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := edit.SetTOMLTable(path, "model_providers."+id, edit.KV{Path: "base_url", Value: "https://relay.example.test/v1"}); err != nil {
		t.Fatal(err)
	}
	table, err := edit.GetTOMLTable(path, "model_providers."+id)
	if err != nil || table["base_url"] != "https://relay.example.test/v1" {
		t.Fatalf("table %v, error %v", table, err)
	}
}

func TestDirectKeepsConfigWhenProvidersUnreadable(t *testing.T) {
	for _, id := range []string{"claude", "codex", "opencode"} {
		t.Run(id, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("CODEX_HOME", "")
			t.Setenv("OPENCODE_CONFIG_DIR", "")
			p := provider.Provider{ID: "relay", Name: "Relay", Key: "secret", Chat: "https://relay.example.test/v1", Anthropic: "https://relay.example.test", Responses: "https://relay.example.test/v1", Models: []string{"one"}}
			if err := provider.Save(p); err != nil {
				t.Fatal(err)
			}
			var a *Agent
			switch id {
			case "claude":
				a = claude(home)
			case "codex":
				a = codex(home)
			default:
				a = opencode(home, filepath.Join(home, ".config"))
			}
			if err := os.MkdirAll(filepath.Dir(a.Path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := a.Connect(); err != nil {
				t.Fatal(err)
			}
			tick(t, "relay", id)
			if err := a.Sync(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(a.Path)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{provider.Path(), provider.Path() + ".bak"} {
				if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Sync(); err == nil {
				t.Fatal("unreadable providers file was treated as an empty list")
			}
			after, err := os.ReadFile(a.Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("direct configuration changed while providers were unreadable", err)
			}
		})
	}
}
