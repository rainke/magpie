package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/edit"
	"github.com/yetone/magpie/internal/provider"
)

func TestClaudeDirectPickerOnlyListsBoundProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	deepseek := provider.Provider{ID: "deepseek", Name: "DeepSeek", Key: "test-key", Anthropic: "https://deepseek.example.test/anthropic", Models: []string{"deepseek-chat", "deepseek-reasoner"}}
	other := provider.Provider{ID: "another", Name: "Another", Key: "another-key", Anthropic: "https://another.example.test", Models: []string{"other-model"}}
	for _, p := range []provider.Provider{deepseek, other} {
		if err := provider.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	a := claude(home)
	if err := os.MkdirAll(filepath.Dir(a.Path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	picker := func() (struct {
		Replace bool `json:"replaceBuiltInOptions"`
		Options []struct {
			Model string `json:"model"`
		} `json:"options"`
	}, []string) {
		t.Helper()
		var got struct {
			Replace bool `json:"replaceBuiltInOptions"`
			Options []struct {
				Model string `json:"model"`
			} `json:"options"`
		}
		var allowed []string
		raw, _ := edit.GetJSONRaw(a.Path, "modelPicker")
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("picker: %v, %s", err, raw)
		}
		raw, _ = edit.GetJSONRaw(a.Path, "availableModels")
		if err := json.Unmarshal([]byte(raw), &allowed); err != nil {
			t.Fatalf("allowlist: %v, %s", err, raw)
		}
		return got, allowed
	}
	tick(t, "deepseek", "claude")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	got, allowed := picker()
	var ids []string
	for _, o := range got.Options {
		ids = append(ids, o.Model)
	}
	if !got.Replace || !slices.Equal(ids, deepseek.Models) || !slices.Equal(allowed, deepseek.Models) {
		t.Fatalf("direct picker = %v, replace=%v, allowed=%v; want only %v", ids, got.Replace, allowed, deepseek.Models)
	}
	// A model added or removed on the bound provider follows the next sync.
	deepseek.DirectAgents = []string{"claude"}
	deepseek.Models = []string{"deepseek-chat", "deepseek-new"}
	if err := provider.Save(deepseek); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	got, allowed = picker()
	ids = nil
	for _, o := range got.Options {
		ids = append(ids, o.Model)
	}
	if !slices.Equal(ids, deepseek.Models) || !slices.Equal(allowed, deepseek.Models) {
		t.Fatalf("after update picker=%v allowed=%v", ids, allowed)
	}
	// Unticking restores the gateway list, including the other provider.
	tick(t, "deepseek")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	raw, _ := edit.GetJSONRaw(a.Path, "modelPicker")
	var restored struct {
		Options []struct {
			Model string `json:"model"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(raw), &restored); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(restored.Options, func(o struct {
		Model string `json:"model"`
	}) bool {
		return o.Model == "another/other-model"
	}) {
		t.Fatalf("gateway picker not restored: %s", raw)
	}
	if _, ok := edit.GetJSONRaw(a.Path, "availableModels"); ok {
		t.Fatal("direct allowlist remained after unticking")
	}
}

func TestClaudeDirectPickerPreservesUserSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	p := provider.Provider{ID: "deepseek", Name: "DeepSeek", Key: "key", Anthropic: "https://deepseek.example.test", Models: []string{"deepseek-chat", "deepseek-reasoner"}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	a := claude(home)
	writeFile(t, a.Path, `{"modelPicker":{"options":[{"model":"my-model","label":"Mine","description":"my own"}]},"availableModels":["deepseek/deepseek-chat"]}`)
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	beforePicker, _ := edit.GetJSONRaw(a.Path, "modelPicker")
	beforeAllowed, _ := edit.GetJSONRaw(a.Path, "availableModels")
	tick(t, "deepseek", "claude")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	raw, _ := edit.GetJSONRaw(a.Path, "availableModels")
	var allowed []string
	if err := json.Unmarshal([]byte(raw), &allowed); err != nil || !slices.Equal(allowed, []string{"deepseek-chat"}) {
		t.Fatalf("widened the user's allowlist: %s (%v)", raw, err)
	}
	if err := a.Pick("model", "deepseek/deepseek-reasoner"); err == nil {
		t.Fatal("allowed a model the user restricted")
	}
	tick(t, "deepseek")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	picker, _ := edit.GetJSONRaw(a.Path, "modelPicker")
	allowedRaw, _ := edit.GetJSONRaw(a.Path, "availableModels")
	if !sameJSON(picker, json.RawMessage(beforePicker)) || !sameJSON(allowedRaw, json.RawMessage(beforeAllowed)) {
		t.Fatalf("user settings weren't restored: picker=%s allow=%s", picker, allowedRaw)
	}
}

func TestClaudeDirectPickerUpdatesExistingBinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	p := provider.Provider{ID: "deepseek", Name: "DeepSeek", Key: "key", Anthropic: "https://deepseek.example.test", Models: []string{"deepseek-chat", "deepseek-reasoner"}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	a := claude(home)
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	tick(t, "deepseek", "claude")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	// State from the previous release had no record of these fields and left
	// the gateway's full picker in place even after direct binding.
	old := `{"options":[{"model":"another/other-model","label":"Other","description":"via magpie"}]}`
	if err := edit.SetJSON(a.Path, edit.KV{Path: "modelPicker", Value: json.RawMessage(old)}); err != nil {
		t.Fatal(err)
	}
	if err := edit.DelJSON(a.Path, "availableModels"); err != nil {
		t.Fatal(err)
	}
	var state singleDirectState
	if err := json.Unmarshal([]byte(stashLoad()["claude.direct.single"]), &state); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"modelPicker", "availableModels"} {
		delete(state.Before, k)
		delete(state.Last, k)
	}
	encoded, _ := json.Marshal(state)
	stash(map[string]string{"claude.direct.single": string(encoded)})
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	raw, _ := edit.GetJSONRaw(a.Path, "modelPicker")
	var got struct {
		Replace bool `json:"replaceBuiltInOptions"`
		Options []struct {
			Model string `json:"model"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil || !got.Replace || len(got.Options) != 2 || got.Options[0].Model != "deepseek-chat" || got.Options[1].Model != "deepseek-reasoner" {
		t.Fatalf("old binding not updated: %s (%v)", raw, err)
	}
}

func TestClaudeDirectPickerKeepsHandEditedPicker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	p := provider.Provider{ID: "deepseek", Name: "DeepSeek", Key: "key", Anthropic: "https://deepseek.example.test", Models: []string{"deepseek-chat"}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	a := claude(home)
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	tick(t, "deepseek", "claude")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	custom := `{"options":[{"model":"my-model","label":"Mine","description":"my own"}]}`
	if err := edit.SetJSON(a.Path, edit.KV{Path: "modelPicker", Value: json.RawMessage(custom)}); err != nil {
		t.Fatal(err)
	}
	if a.Check() == "" {
		t.Fatal("a changed direct picker was not reported")
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	tick(t, "deepseek")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	got, _ := edit.GetJSONRaw(a.Path, "modelPicker")
	if !sameJSON(got, json.RawMessage(custom)) {
		t.Fatalf("hand-edited picker overwritten: %s", got)
	}
}

func TestClaudeDirectPickerWSL(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	p := provider.Provider{ID: "deepseek", Name: "DeepSeek", Key: "key", Anthropic: "https://deepseek.example.test", Models: []string{"deepseek-chat"}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	d := distro{Name: "Ubuntu", Home: "/home/me", Root: root, Running: true, Mirrored: true}
	a := claudeIn(d.place("claude@wsl:Ubuntu"))
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	tick(t, "deepseek", "claude")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	raw, _ := edit.GetJSONRaw(a.Path, "modelPicker")
	var picker struct {
		Replace bool `json:"replaceBuiltInOptions"`
		Options []struct {
			Model string `json:"model"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(raw), &picker); err != nil || !picker.Replace || len(picker.Options) != 1 || picker.Options[0].Model != "deepseek-chat" {
		t.Fatalf("WSL picker: %s (%v)", raw, err)
	}
	tick(t, "deepseek")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if _, ok := edit.GetJSONRaw(a.Path, "availableModels"); ok {
		t.Fatal("WSL direct allowlist was not removed")
	}
}

func TestClaudeDirectPickerDisconnectRestoresUserSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	p := provider.Provider{ID: "deepseek", Name: "DeepSeek", Key: "key", Anthropic: "https://deepseek.example.test", Models: []string{"deepseek-chat"}}
	if err := provider.Save(p); err != nil {
		t.Fatal(err)
	}
	a := claude(home)
	originalPicker := `{"options":[{"model":"mine","label":"My model","description":"custom"}]}`
	originalAllowed := `["deepseek-chat"]`
	writeFile(t, a.Path, `{"modelPicker":`+originalPicker+`,"availableModels":`+originalAllowed+`}`)
	if err := a.Connect(); err != nil {
		t.Fatal(err)
	}
	tick(t, "deepseek", "claude")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	picker, _ := edit.GetJSONRaw(a.Path, "modelPicker")
	allowed, _ := edit.GetJSONRaw(a.Path, "availableModels")
	if !sameJSON(picker, json.RawMessage(originalPicker)) || !sameJSON(allowed, json.RawMessage(originalAllowed)) {
		t.Fatalf("disconnect changed user settings: picker=%s allowed=%s", picker, allowed)
	}
}
