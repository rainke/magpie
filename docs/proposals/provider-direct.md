# Proposal: a provider the agent reaches directly

Status: partly implemented. OpenCode (and the agents that share its config
shape), Pi, Claude Code and Codex support direct providers. What is built is under [Built so far](#built-so-far);
the rest of this page is the change as a whole. Owner decisions are under
[Decisions](#decisions).

## What the user gets

A key+URL provider can be given to an agent as **the agent's own provider**:
magpie writes the provider's base URL, its first key and the models it serves
into that agent's config, and the agent's requests for those models go from
the agent to the vendor. Nothing goes through magpie's gateway.

The switch lives in the provider's editor (Providers → a provider → Advanced).
It is offered per agent, on the agents that are connected and can hold a
provider entry of their own.

## Decisions

1. **Only a key+URL provider.** A signed-in subscription, a plugin and another
   magpie are out of scope: their requests are signed, refreshed or made by
   magpie itself, so there is nothing to hand the agent.
2. **No protocol mismatch.** A (provider, agent) pair is offered only when the
   provider has a base URL for an API that agent speaks itself. Without one the
   request would need magpie's translation, which direct mode gives up.
3. **The first key only.** The agent's entry gets `Provider.Key`, the key
   magpie's own requests try first. Key rotation, rests, per-key protocol,
   weight, plan and windows do not carry: one key is written and it is the one
   the agent uses until the next Sync.
4. **Agent protocols.** `Agent.Direct` is `{Chat}` for the OpenCode-shaped
   agents (OpenCode, MiMo Code, DevEco Code), since magpie writes their
   provider entry as an openai-compatible chat endpoint. A model the provider
   serves only on Messages or Responses stays in magpie's entry, where the
   gateway translates it. Pi takes Chat, Responses and Messages, one per
   model (`openai-completions`, `openai-responses`, `anthropic-messages`),
   but not Gemini: apps that run Pi's SDK on Pi's own folder hand each
   request a fetch pi-ai's Google adapter refuses, so a Gemini-only model
   stays in magpie's entry. Claude Code speaks Messages and Codex speaks
   Responses; each selects one provider as its default route.
5. **The tick names the agent's catalog id.** `DirectAgents` holds `opencode`,
   `mimocode`, `deveco`, `pi`, `claude` or `codex`, so a WSL twin is written with its Windows twin and
   the entry's models are the same in both.

## Eligibility

`provider.TakesDirectAgents(p)` is the provider's half, modeled on
`TakesInsecureTLS` (one predicate, checked on Save, on the switch and on
Sync). A provider is refused when:

| Refusal | Why |
| --- | --- |
| `p.Account != nil` | a subscription signs, refreshes and rotates its own tokens |
| `p.IsPlugin()` | the plugin host makes the request |
| `p.IsRemoteMagpie()` | another magpie is out of scope (Decision 1) |
| `p.DecideOnly()` | it only serves magpie's decision calls |
| `p.Off` | a switched-off provider serves nothing, so nothing is written |
| `p.Key == ""` | there is no key to write |
| no `p.Speaks()` ∩ `a.Direct` | no API in common (Decision 2) |
| `p.KeyProtocol` set and ≠ the chosen API | the first key is restricted to another API |
| `p.Proxy != ""` | the user reaches it through a proxy the agent would not use |
| `p.Headers` or `p.ExtraBody` set | the request's shape is the user's, and the agent can't be given it |
| `p.AuthHeader` is `AuthXAPIKey` or `AuthBearer` | same: magpie chooses the auth header |
| `p.PassUserAgent` | magpie sends the agent's own User-Agent; direct sends the agent's anyway, but a vendor locked to it was locked to magpie's relay |
| `p.PinUpstream` | magpie pins the vendor's own upstream |
| `p.InsecureTLS` | the agent checks the certificate; magpie was told not to |

Each refusal carries its reason, and the editor's fold says it in words rather
than hiding the row (§ "The editor" below).

The agent's half is a capability list on its spec:

```go
// Direct are the APIs this agent's own provider entries can speak, in its
// own format (OpenCode's, Pi's, Droid's). nil: it takes none.
Direct []provider.Protocol
```

The chosen API for (provider, agent) is the first of `p.Speaks()` that appears
in `a.Direct` **and** that `p.KeyProtocol` allows. Where the agent's format
takes an API per model (Pi's `api` and `baseUrl` per model, Droid's
`provider`, Grok Build's `api_backend`), the entry is chosen per model instead
— the first of `p.APIs(m.ID)` ∩ `p.Speaks()` ∩ `a.Direct` — and a model with
none stays in magpie's list, where the gateway translates it. `p.APIs` is the
same lookup `piModelJSON`, `droidWants` and `grokBackend` already use.

## State

```go
// DirectAgents names the agents this provider is written into as their own
// provider, so their requests go to it rather than through magpie
// (`directAgents`). An agent that isn't connected, is unknown, or can no
// longer speak one of the provider's APIs is left out (see TakesDirectAgents).
// Only a key+URL provider takes it.
DirectAgents []string `json:"directAgents,omitempty"`
```

- Additive: a `providers.json` without the field behaves exactly as today.
- Stored on the provider, because the address, the key and the models are its.
- Ticked per agent, because writing a key into an agent's file is the user's
  decision, and because some agents can hold only one provider (Claude Code's
  `ANTHROPIC_BASE_URL`, Codex's `model_provider`) — those accept a single binding and temporarily replace magpie's own wiring.
- A providers.json that can't be read is **unknown**, never "no direct
  agents": no entry is written or removed, and Drift says so. `lastgood`
  applies as for every other state file.

## What magpie writes

For each agent in `DirectAgents`, the agent's own config gets one entry magpie
owns: id **`magpie-direct-<provider id>`** (the agent's own naming shape; the
id can't collide with magpie's `magpie` entry or with a user's). It holds:

- the base URL: `p.Base(api)` — the API the provider speaks, not magpie's;
- the key: `p.Key`, verbatim (not the gateway key);
- the models: `p.Exposed()` less the models `settings.DisabledModels` turns
  off (`entriesOff`'s rule) less the models not served on `api`, each with the
  window, reply limit, price and effort levels magpie already writes for that
  agent (`magpieProviderJSONAt`'s per-model entries, `piModelJSON`, `droid`'s
  `customModels`, `grokBackend`'s tables);
- a display name of the provider's (`p.Name`), so two providers' copies of one
  model are told apart in the agent's picker.

Writes follow the rules every other wiring follows:

- **`theirsKept` / the `applied` stash.** What magpie last wrote for
  `direct.<provider id>` is kept per agent. A value the user changed since is
  theirs: Sync reports it and leaves it. A Sync with nothing to change writes
  nothing.
- **Disconnect** takes the entry out and keeps an entry the user made under a
  name of their own. Taking the tick off, deleting the provider or switching
  it off removes the entry on the next Sync.
- **WSL twins** are written and removed with the agent, as today.
- **A key or address the user changes** is rewritten on the next Sync. The
  agent running at that moment keeps the old value until it is restarted;
  agents that read their config once at start-up say so in their `Notice`.

## What magpie's list shows that agent

The direct models are **left out of the magpie entry** of a ticked agent, so
one model has one entry in its picker. This is the choke point per agent that
builds magpie's list (`magpieModels` / the `models` block per agent); the
gateway's own `/v1/models` is unchanged.

So direct mode is offered only on agents whose model list magpie writes into a
file. Agents that ask the gateway for their list (Cursor Private Inference,
Empryo and the like) are not offered it in this change: the gateway can't tell
which agent a list request is for, so it can't leave a model out for one.

## The gateway is unchanged

A request for a directly-wired model that still reaches magpie — an old
session, another agent, a routing group a user put it in, a client that asks
`/v1/models` — is served exactly as today. Direct mode toggles wiring; it never
takes a provider out of magpie. That keeps the change reversible: unticking the
agent and a Sync puts everything back.

## The editor

Providers → a provider → Advanced → **Direct in agents**:

- the fold appears only for a provider `TakesDirectAgents` accepts;
- a refused provider says why in one line (the table above), so an empty fold
  is not a mystery;
- one line per connected agent with a `Direct` capability; a line is offered
  only when an API matches (Decision 2), and one that doesn't says which APIs
  the agent speaks and which the provider serves;
- the note under the fold says, in the user's words: the address and the first
  key are written into the agent's own config; magpie's routing, fallbacks,
  redaction, usage, limits, rests and quota reads no longer apply to those
  requests; the key is written in the clear, as that agent keeps its own; only
  the first key is written when the provider holds several;
- `POST /api/provider/save` takes `directAgents`, an array of agent ids; an
  unknown id or one the provider can't serve is refused, not silently dropped.

Naming: the proxy picker already calls its no-proxy value **Direct** ("Requests
to it go direct, whatever the proxy in Settings"). To keep two "Direct"s from
meaning two things in one editor, that picker's option becomes **No proxy**
(`t("No proxy")`, `t("Through no proxy")`), and this fold keeps **Direct in
agents**. Strings in en, zh, zh-TW, ja, de.

A CLI (`magpie provider direct <id> on|off [agent…]`, listed by
`magpie provider`) is a follow-up, not part of this change.

## Built so far

- `provider.DirectAgents`, `DirectOffered`, `DirectRefusal`, `DirectAPI`,
  `DirectModels`, `DirectModelList`, `DirectID`/`DirectOf` and
  `NormalDirectAgents` ([`direct.go`](../../internal/provider/direct.go)); the
  field is kept by a save that leaves it out.
- `agent.Direct` on the OpenCode-shaped agents; `syncDirectOpenCode` writes
  `provider.magpie-direct-<id>` and removes the ones no longer wanted,
  `directModels` leaves those models out of magpie's entry, `directValue`
  makes a model picked on one count as magpie's (so it is `Wired`, and
  Disconnect is offered), and `Unwire` takes the entries out on Disconnect
  ([`direct.go`](../../internal/agent/direct.go)).
- Pi takes any number of providers, each model on its own API and address:
  `syncDirectPi` writes `providers.magpie-direct-<id>` in models.json with
  Pi's own model entry fields, `piDirectIDs` leaves those models out of
  `providers.magpie`, and `piScopeDirectWithout` takes a gone entry's refs
  out of `enabledModels`
  ([`pi_direct.go`](../../internal/agent/pi_direct.go)). A WSL distro's Pi is
  written at the same place in its own home.
- The provider editor's `Direct in agents` fold, its refusals and its ticks
  (`directField` in `app.js`), `POST /api/provider/save` taking
  `directAgents`, and `GET /api/providers` telling the editor
  `directOffered`, `directRefusal` and one `direct` row per agent.
- Tests: `TestDirect*` in `internal/provider`, `TestOpenCodeDirect*` and
  `TestPiDirect*` in `internal/agent`, `TestProviderDirectAgents` in
  `internal/gui`, and `internal/gui/tests/provider-direct.test.cjs`.
- Claude Code and Codex have a single direct provider, with their previous route restored when the binding is removed; see [agent wiring](../subsystems/agent-wiring.md).
- Not built yet: the remaining agents; the CLI; a filter of gateway-served
  model lists; and the **No proxy** rename of the proxy picker's no-proxy
  value, so the editor still has two values called Direct.

## Verification

Go tests (`internal/provider`, `internal/agent`, `internal/gui`):

- `TestTakesDirectAgents` — one case per refusal above, and a remote magpie
  among them; a provider that passes with each of Chat, Messages and Responses.
- `TestDirectAgentsOffByDefault` — nothing is written for any agent.
- `Test<Agent>DirectWritesTheProvider` — base URL, key and models in the
  agent's own format, per API; the models are gone from the magpie entry.
- `Test<Agent>DirectSyncIsIdempotent` — a second Sync writes nothing (bytes
  compared), keeping a user's edit of that entry.
- `Test<Agent>DirectDisconnectKeepsTheUsersEntry` — and removes magpie's.
- `TestDirectDriftSeesAChangedKey` — a key changed by hand is reported, not
  overwritten.
- `TestDirectFirstKeyOnly` — a provider with three keys writes `p.Key` alone.
- `TestDirectModelOffThatAPIStaysOnMagpie` — a Messages-only Claude on a
  Chat-only provider stays in magpie's list and is not in the direct entry.
- `TestDirectProviderOffRemovesTheEntry`.

Each new test is run against the code without the change and must fail. GUI:
`provider-direct.test.cjs` in Chromium and WebKit, in every language, plus the
i18n key guard. `gofmt -l`, `go vet ./...`, the linux/windows vets and builds,
`go test -tags nogui ./...` on a machine running one suite at a time.

Real use: a real relay key, on the first agent, in a sandbox HOME —
a streamed turn that answers 200 from the vendor (its own logs show the
request), the same turn after rotating the key and a Sync, then Disconnect and
a turn through magpie again. A provider whose only base is Messages is tried
on an agent that speaks Messages and refused on one that speaks Chat only.
What wasn't tried is said; a platform-specific wiring is tried on that
platform.

## Slices

1. `provider: a provider names the agents that use it directly` — the field,
   `TakesDirectAgents`, the editor fold and its refusals, i18n. **Built.**
2. `agent: OpenCode takes a provider directly` — spec `Direct`, the entry
   written and removed, the list subtraction, Disconnect, end to end.
   **Built**, for every OpenCode-shaped agent at once.
3. One commit per further agent, each with its own tests; WSL in the same
   commit as its agent. Claude Code and Codex are **built** as single-upstream
   bindings; Pi is **built** with an API per model.
4. Docs: `agent-wiring.md` gains "A provider of the agent's own", and
   `providers-accounts.md` gains the field and the eligibility table.

## Risks, stated up front

- **The key is on disk in the agent's config**, outside magpie's file and its
  backups. Disconnect removes magpie's entry; a backup of the agent's own
  config may still hold the key.
- **No fallback.** A dead provider, a spent key or a rate limit is the agent's
  own error. That is the point of the mode, and the note says so.
- **No redaction.** Requests no longer pass Settings → Privacy. A user who
  turned it on for that provider loses it for that agent.
- **Two sources of truth for a model's price and window.** The agent gets the
  values as they are when it is written; a later change to the model on the
  vendor's list reaches it at the next Sync, not at once.
- **A vendor that locks a plan to an agent's User-Agent** sees that agent
  directly. `PassUserAgent` is refused, so a provider that needed magpie to
  relay a different identity can't be direct — by design.
