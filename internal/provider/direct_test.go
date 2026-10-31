package provider

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/plugin"
)

// A provider of the user's own may be written into an agent's own config
// (see docs/proposals/provider-direct.md). These are the two halves the
// editor and the agent's Sync ask: whether this provider can be handed over
// whole, and on which API, with which models.

func TestDirectOffered(t *testing.T) {
	key := Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example.test/v1", Key: "k"}
	cases := []struct {
		name string
		p    Provider
		want bool
	}{
		{"a provider of the user's", key, true},
		{"a switched-off provider", func() Provider { p := key; p.Off = true; return p }(), false},
		{"a subscription", Provider{ID: "codex", Name: "Codex", Account: &Account{Agent: "codex"}}, false},
		{"a plugin's", Provider{ID: "cursor", Name: "Cursor", Account: &Account{Agent: "cursor", plugin: &plugin.Provider{ID: "cursor"}}}, false},
		{"another magpie", Provider{ID: "far", Name: "Far", Preset: RemoteMagpiePreset}, false},
		{"a decision API", Provider{ID: "jev", Name: "Jev", Decide: "https://jev.example.test/v1"}, false},
	}
	for _, c := range cases {
		if got := c.p.DirectOffered(); got != c.want {
			t.Errorf("%s: DirectOffered %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDirectRefusal(t *testing.T) {
	base := Provider{ID: "relay", Name: "Relay", Chat: "https://relay.example.test/v1", Key: "k"}
	with := func(f func(*Provider)) Provider {
		p := base
		f(&p)
		return p
	}
	if why := base.DirectRefusal(); why != "" {
		t.Fatalf("a provider of the user's refused: %s", why)
	}
	cases := []struct {
		name string
		p    Provider
		want string
	}{
		{"no key", with(func(p *Provider) { p.Key = "" }), "needs an API key"},
		{"a proxy of its own", with(func(p *Provider) { p.Proxy = "http://127.0.0.1:7890" }), "proxy"},
		{"headers of the user's", with(func(p *Provider) { p.Headers = map[string]string{"X-Tenant": "acme"} }), "headers"},
		{"an extra body", with(func(p *Provider) { p.ExtraBody = ExtraBody{"service_tier": json.RawMessage(`"flex"`)} }), "body"},
		{"one auth header", with(func(p *Provider) { p.AuthHeader = AuthXAPIKey }), "auth header"},
		{"the agent's User-Agent", with(func(p *Provider) { p.PassUserAgent = true }), "User-Agent"},
		{"a pinned upstream", with(func(p *Provider) { p.PinUpstream = true }), "pinned"},
		{"an unchecked certificate", with(func(p *Provider) { p.InsecureTLS = true }), "unchecked"},
		{"Vertex AI", with(func(p *Provider) { p.Preset = VertexPreset }), "Google credentials"},
	}
	for _, c := range cases {
		why := c.p.DirectRefusal()
		if !strings.Contains(why, c.want) {
			t.Errorf("%s: refused with %q, want it to say %q", c.name, why, c.want)
		}
		if c.p.TakesDirectAgents() {
			t.Errorf("%s: TakesDirectAgents said yes", c.name)
		}
	}
}

func TestDirectAPI(t *testing.T) {
	p := Provider{
		ID: "relay", Name: "Relay", Key: "k",
		Chat: "https://relay.example.test/v1", Responses: "https://relay.example.test/v1",
		Anthropic: "https://relay.example.test",
	}
	cases := []struct {
		name   string
		protos []Protocol
		key    Protocol
		want   Protocol
	}{
		{"the agent speaks messages and responses", []Protocol{Anthropic, Responses}, "", Responses},
		{"responses alone", []Protocol{Responses}, "", Responses},
		{"no API in common", []Protocol{Gemini}, "", ""},
		{"the first key is for messages", []Protocol{Chat, Anthropic}, Anthropic, Anthropic},
		{"the first key is for an API the agent doesn't speak", []Protocol{Chat}, Responses, ""},
	}
	for _, c := range cases {
		q := p
		q.KeyProtocol = c.key
		if got := q.DirectAPI(c.protos); got != c.want {
			t.Errorf("%s: DirectAPI %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDirectModels(t *testing.T) {
	chat, anth := "https://relay.example.test/v1", "https://relay.example.test"
	if err := Save(Provider{
		ID: "relay", Name: "Relay", Key: "k", Chat: chat, Anthropic: anth,
		Models: []string{"chat-only", "claude", "any"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("relay", chat, []catalog.Model{
		{ID: "chat-only", APIs: []string{string(Chat)}},
		{ID: "claude", APIs: []string{string(Anthropic)}},
		{ID: "any"},
	}); err != nil {
		t.Fatal(err)
	}
	p, err := Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	ids := func(api Protocol) []string {
		var out []string
		for _, m := range p.DirectModels(api) {
			out = append(out, m.ID)
		}
		return out
	}
	if got, want := ids(Chat), []string{"chat-only", "any"}; !slices.Equal(got, want) {
		t.Errorf("chat: %v, want %v", got, want)
	}
	if got, want := ids(Anthropic), []string{"claude", "any"}; !slices.Equal(got, want) {
		t.Errorf("messages: %v, want %v", got, want)
	}
	// a model the user disabled is not handed to an agent either
	if err := SetDisabledModels([]string{"relay/chat-only"}); err != nil {
		t.Fatal(err)
	}
	defer SetDisabledModels(nil)
	p, _ = Find("relay")
	if got := ids(Chat); !slices.Equal(got, []string{"any"}) {
		t.Errorf("with chat-only disabled: %v, want [any]", got)
	}
}

// DirectModelList is DirectModels for an agent whose entry takes an API per
// model (Pi's api and baseUrl per model): each model on the first API it is
// served on out of the ones the agent speaks, or none, which stays on
// magpie. The first key's protocol still narrows it.
func TestDirectModelList(t *testing.T) {
	chat, anth := "https://relay.example.test/v1", "https://relay.example.test"
	if err := Save(Provider{
		ID: "relay", Name: "Relay", Key: "k", Chat: chat, Responses: chat, Anthropic: anth,
		Models: []string{"chat-only", "claude", "both", "any"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("relay", chat, []catalog.Model{
		{ID: "chat-only", APIs: []string{string(Chat)}},
		{ID: "claude", APIs: []string{string(Anthropic)}},
		{ID: "both", APIs: []string{string(Anthropic), string(Chat)}},
		{ID: "any"},
	}); err != nil {
		t.Fatal(err)
	}
	p, err := Find("relay")
	if err != nil {
		t.Fatal(err)
	}
	got := func() []string {
		var out []string
		for _, dm := range p.DirectModelList([]Protocol{Chat, Anthropic}) {
			out = append(out, dm.Model.ID+"@"+string(dm.API))
		}
		return out
	}
	if want := []string{"chat-only@chat", "claude@anthropic", "both@anthropic", "any@chat"}; !slices.Equal(got(), want) {
		t.Errorf("%v, want %v", got(), want)
	}
	q := *p
	q.KeyProtocol = Anthropic
	if got := func() []string {
		var out []string
		for _, dm := range q.DirectModelList([]Protocol{Chat, Anthropic}) {
			out = append(out, dm.Model.ID+"@"+string(dm.API))
		}
		return out
	}(); !slices.Equal(got, []string{"claude@anthropic", "both@anthropic", "any@anthropic"}) {
		t.Errorf("first key on messages: %v", got)
	}
	// an agent that speaks Gemini alone gets none of these
	if ms := p.DirectModelList([]Protocol{Gemini}); len(ms) != 0 {
		t.Errorf("Gemini: %v", ms)
	}
}

func TestNormalDirectAgents(t *testing.T) {
	got := NormalDirectAgents([]string{" opencode ", "", "opencode", "mimocode"})
	if want := []string{"opencode", "mimocode"}; !slices.Equal(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
	if NormalDirectAgents(nil) != nil {
		t.Error("none became a list")
	}
}

func TestDirectID(t *testing.T) {
	if got := DirectID("relay"); got != "magpie-direct-relay" {
		t.Errorf("%q", got)
	}
	if got := DirectOf("magpie-direct-relay"); got != "relay" {
		t.Errorf("%q", got)
	}
	for _, id := range []string{"magpie", "magpie-direct-", "ollama"} {
		if got := DirectOf(id); got != "" {
			t.Errorf("DirectOf(%q) = %q, want none", id, got)
		}
	}
}
