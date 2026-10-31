package provider

import (
	"cmp"
	"slices"
	"strings"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/settings"
)

// A provider of the user's own can be written into an agent's own config as
// that agent's provider, so the agent asks it directly rather than through
// magpie's gateway: the address magpie would send to, the first key magpie
// would sign with, and the models it serves there. The editor offers it per
// agent (Provider.DirectAgents), and the agent's own Sync writes and removes
// the entry (see docs/proposals/provider-direct.md).
//
// What it gives up is magpie's part: routing, fallbacks, redaction, usage,
// limits and rests. What it can't carry is a request whose shape magpie
// shapes (headers, an extra body, one auth header, the agent's User-Agent,
// an upstream pin, an unchecked certificate) or a proxy of the provider's
// own; such a provider says why instead.

// DirectPrefix begins the id magpie's entry has in an agent's config for a
// provider: "magpie-direct-<provider id>". An agent's own provider of that
// name is magpie's, as its "magpie" entry is.
const DirectPrefix = "magpie-direct-"

// DirectID is the entry id of the provider pid in an agent's config.
func DirectID(pid string) string { return DirectPrefix + pid }

// DirectOf is the provider id an agent's entry id names, when magpie wrote
// the entry (DirectID): "" for another entry.
func DirectOf(id string) string {
	pid, ok := strings.CutPrefix(id, DirectPrefix)
	if !ok || pid == "" {
		return ""
	}
	return pid
}

// DirectOffered reports whether the editor shows the direct-in-agents fold
// for p at all: a provider of the user's own that serves something. A
// signed-in subscription, a plugin, another magpie, a decision-only API and
// a switched-off provider have nothing to hand an agent.
func (p Provider) DirectOffered() bool {
	return p.Account == nil && !p.IsPlugin() && !p.IsRemoteMagpie() && !p.DecideOnly() && !p.Off
}

// DirectRefusal is why p can't be written into an agent's own config, "" when
// it can. Each reason is one the user can act on.
func (p Provider) DirectRefusal() string {
	if !p.DirectOffered() {
		return ""
	}
	switch {
	case p.IsVertex():
		return p.Name + " is asked with your Google credentials, which an agent's own provider can't be given"
	case p.Key == "":
		return p.Name + " needs an API key to be given to an agent"
	case p.Proxy != "":
		return p.Name + " goes through a proxy of its own, which the agent wouldn't use"
	case len(p.Headers) > 0:
		return p.Name + " sends headers of yours with every request, which the agent's own provider can't be given"
	case len(p.ExtraBody) > 0:
		return p.Name + " adds fields of yours to every request's body, which the agent's own provider can't be given"
	case p.AuthHeader != AuthBoth:
		return p.Name + " sends its key in one auth header, which the agent's own provider can't be told"
	case p.PassUserAgent:
		return p.Name + " is sent the agent's own User-Agent, which direct requests send anyway: its vendor would see another identity"
	case p.PinUpstream:
		return p.Name + " has its models pinned to a vendor's own API, which an agent can't be told"
	case p.InsecureTLS:
		return p.Name + "'s certificate is taken unchecked, which the agent would check"
	}
	return ""
}

// TakesDirectAgents reports whether p can be written into an agent's own
// config (DirectRefusal is empty).
func (p Provider) TakesDirectAgents() bool { return p.DirectRefusal() == "" }

// DirectAPI is the API p is written on into an agent that speaks protos: the
// first of p.Speaks() the agent speaks itself, which p's first key may be
// used on (KeyProtocol); "" when there is none, and the editor offers the
// agent nothing.
func (p Provider) DirectAPI(protos []Protocol) Protocol {
	for _, pr := range p.Speaks() {
		if !slices.Contains(protos, pr) {
			continue
		}
		if p.KeyProtocol != "" && p.KeyProtocol != pr {
			continue
		}
		return pr
	}
	return ""
}

// DirectModels is p's models as an agent's own provider entry carries them on
// api: those p serves there (APIs), less the disabled ones (ModelDisabled),
// each with the window, reply limit, levels and images the agent's list
// carries, under the vendor's own id, which is what a direct request names.
// The price is the vendor's list price, not what magpie counts: no price rate
// or agent pricing stands between the agent and the vendor.
func (p Provider) DirectModels(api Protocol) []catalog.Model {
	var out []catalog.Model
	for _, dm := range p.DirectModelList([]Protocol{api}) {
		if dm.API == api {
			out = append(out, dm.Model)
		}
	}
	return out
}

// DirectModel is one of a provider's models as an agent whose own entry takes
// an API per model carries it (Pi's api and baseUrl per model): the model,
// and the API it is written on.
type DirectModel struct {
	Model catalog.Model
	API   Protocol
}

// DirectModelList lists p's models as an agent that speaks protos takes them
// when its entry carries an API per model (Pi's api and baseUrl per model),
// in p's list order: each on the first of its APIs that agent speaks and
// p's first key may be used on, less the disabled ones. A model with no such
// API is left out, staying in magpie's entry, where the gateway translates
// it. DirectModels is this for one API.
func (p Provider) DirectModelList(protos []Protocol) []DirectModel {
	s := settings.Load()
	var out []DirectModel
	for _, m := range p.Exposed() {
		if _, off := ModelDisabled(p.ID, m.ID); off {
			continue
		}
		api := p.directModelAPI(m.ID, protos)
		if api == "" {
			continue
		}
		ctx := p.ContextOf(m.ID)
		if ctx == 0 {
			ctx = m.Context
		}
		out = append(out, DirectModel{API: api, Model: catalog.Model{
			ID: m.ID, Name: cmp.Or(m.Name, m.ID), Context: ctx, Output: p.replyLimit(m, s),
			Efforts: m.Efforts, Reasoning: m.Reasoning, Images: m.Images, Price: m.Price,
			Released: m.Released,
		}})
	}
	return out
}

// directModelAPI is the API model is written on into an agent that speaks
// protos: the first of its APIs (APIs) that the agent speaks and p's first
// key may be used on, p.Speaks() when the model's APIs aren't known; "" when
// there is none.
func (p Provider) directModelAPI(model string, protos []Protocol) Protocol {
	apis := p.APIs(model)
	if len(apis) == 0 {
		apis = p.Speaks()
	}
	for _, pr := range apis {
		if !slices.Contains(protos, pr) || p.Base(pr) == "" {
			continue
		}
		if p.KeyProtocol != "" && p.KeyProtocol != pr {
			continue
		}
		return pr
	}
	return ""
}

// ProtocolName is an API's name as the editor and its errors say it: chat,
// responses, messages, gemini.
func ProtocolName(pr Protocol) string {
	switch pr {
	case Chat:
		return "chat"
	case Responses:
		return "responses"
	case Anthropic:
		return "messages"
	case Gemini:
		return "gemini"
	}
	return string(pr)
}

// ProtocolNames is each of ps by ProtocolName, comma separated.
func ProtocolNames(ps []Protocol) string {
	out := make([]string, len(ps))
	for i, pr := range ps {
		out[i] = ProtocolName(pr)
	}
	return strings.Join(out, ", ")
}

// NormalDirectAgents is agents as DirectAgents is kept: trimmed, empty ones
// out, no repeats, in the order the user picked them.
func NormalDirectAgents(agents []string) []string {
	var out []string
	for _, a := range agents {
		a = strings.TrimSpace(a)
		if a == "" || slices.Contains(out, a) {
			continue
		}
		out = append(out, a)
	}
	return out
}
