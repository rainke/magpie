// Run with Node's test runner and Playwright on the module path; see README.md.
// A provider of the user's own can be written into an agent as its own
// provider, asked directly (docs/proposals/provider-direct.md). Under
// Advanced, "Direct in agents" lists the agents that can take it, one tick
// each, with the note that magpie's routing, fallbacks, redaction, usage,
// limits and rests no longer apply; a ticked agent is saved as
// directAgents, and one the provider can't be written into says why and
// can't be ticked. A provider magpie can't hand over whole says why
// instead of the ticks, a preset's has no fold, and a Save from a page
// without the fold sends none. Every language, wide and narrow.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const base = {
  icon: "generic", chat: "", responses: "", anthropic: "", catalog: "",
  models: [{ id: "m1", name: "m1", on: true }], agents: [], fallback: [], headers: {},
  key: { set: true, masked: "sk-…agg" }, keyList: [], balanceToken: { takes: false, set: false }, proxy: "", searches: false,
};
const relay = {
  ...base, id: "relay", name: "Relay", chat: "https://relay.example.test/v1",
  directOffered: true, direct: [{ id: "opencode", name: "OpenCode", api: "chat" }, { id: "claude", name: "Claude Code", api: "anthropic" }], directAgents: [],
};
const ticked = { ...relay, id: "ticked", name: "Ticked Relay", directAgents: ["opencode"] };
const proxied = {
  ...relay, id: "proxied", name: "Proxied Relay", proxy: "http://127.0.0.1:7890", directAgents: [],
  directRefusal: "Proxied Relay goes through a proxy of its own, which the agent wouldn't use",
};
const blocked = {
  ...relay, id: "blocked", name: "Blocked Relay", directAgents: [],
  direct: [{ id: "opencode", name: "OpenCode", why: "OpenCode speaks chat, and Blocked Relay serves messages" }],
};
const preset = { ...base, id: "openai", name: "OpenAI", preset: "openai", chat: "https://api.openai.example/v1" };

const DIRECT_NOTE = "Address and first key go into the agent's own config, and those requests go straight to it: magpie's routing, fallbacks, redaction, usage and limits don't apply.";
const RESTART = "{agent} reads its model list as it starts: quit and reopen it after a change";
const strings = ["Direct in agents", "Write it into {agent}", DIRECT_NOTE, RESTART];

function serve(lang, posts) {
  const providers = {
    providers: [relay, ticked, proxied, blocked, preset],
    presets: [{ id: "openai", name: "OpenAI", kind: "vendor", icon: "openai", chat: "https://api.openai.example/v1", added: true }],
    excluded: [], gateway: { running: true, window: true },
  };
  const state = { agents: [], profiles: [], settings: { lang, theme: "light" } };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/providers") return json(providers);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname.startsWith("/api/provider/") && route.request().method() === "POST") {
      posts.push({ action: url.pathname.slice("/api/provider/".length), body: route.request().postDataJSON() });
      return json(providers);
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const width of [1100, 420]) {
      const open = async (t, name) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
        let page;
        t.after(async () => {
          if (process.env.ARTIFACT_DIR && page) {
            await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
            await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-${width}-${name}-direct.png`), fullPage: true });
          }
          await browser.close();
        });
        page = await (await browser.newContext({ viewport: { width, height: 760 }, reducedMotion: "reduce" })).newPage();
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(60000);
        const errors = [];
        page.on("pageerror", (e) => errors.push(e.message));
        const posts = [];
        await page.route("**/*", serve(lang, posts));
        await page.goto("http://magpie.test/?view=providers");
        const w = await page.evaluate(([lang, ks]) => Object.fromEntries(ks.map((k) => [k, (I18N[lang] || {})[k] || k])), [lang, [...strings, "Save"]]);
        const selected = [relay, ticked, proxied, blocked, preset].find((p) => p.name === name);
        assert.ok(selected, `unknown fixture ${name}`);
        await page.locator(`.row.provider[data-id="${selected.id}"]`).click();
        await page.locator(".editor .bar").waitFor();
        return { page, errors, posts, w };
      };
      const adv = (page) => page.locator(".editor details.advanced");
      const unfold = async (page) => {
        await adv(page).waitFor({ state: "attached" });
        if (!(await adv(page).evaluate((d) => d.open))) await adv(page).locator(":scope > summary").click();
      };
      const fold = (page) => page.locator(".editor .direct-agents");
      const row = (page, w) => page.locator(".editor label.tick.direct-agent", { hasText: w["Write it into {agent}"].replace("{agent}", "OpenCode") });
      const box = (page, w) => row(page, w).locator("input[type=checkbox]");
      const save = async (page, posts, w) => {
        const n = posts.length;
        await page.locator(".editor .bar").getByRole("button", { name: w.Save, exact: true }).click();
        for (let i = 0; i < 50 && posts.length === n; i++) await page.waitForTimeout(50);
        return posts.at(-1);
      };

      test(`${engine} ${lang} ${width}: OpenCode is offered, ticked and saved as the agent it is written into`, async (t) => {
        const { page, errors, posts, w } = await open(t, "Relay");
        if (lang !== "en") {
          const missing = await page.evaluate(([lang, ks]) => ks.filter((k) => !(I18N[lang] || {})[k]), [lang, strings]);
          assert.deepEqual(missing, [], `every string has its ${lang}`);
        }
        // the note alone doesn't open Advanced; a tick does
        assert.equal(await adv(page).evaluate((d) => d.open), false, "Advanced stays folded while nothing is ticked");
        await unfold(page);
        assert.equal(await page.locator(".editor", { hasText: w[DIRECT_NOTE] }).count() > 0, true, "the note is shown");
        assert.equal(await fold(page).getByText(w[RESTART].replace("{agent}", "Claude Code"), { exact: true }).count(), 1, "Claude Code's restart advice is shown");
        assert.equal(await row(page, w).isVisible(), true);
        assert.equal(await box(page, w).isChecked(), false, "nothing ticked yet");
        assert.equal(await page.locator(".editor select").count(), 0, "no native select");
        await box(page, w).scrollIntoViewIfNeeded();
        const r = await row(page, w).boundingBox();
        assert.ok(r.width > 60 && r.x >= 0 && r.x + r.width <= width, `the row fits: ${JSON.stringify(r)}`);
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, "no sideways scroll");
        await box(page, w).check();
        const saved = await save(page, posts, w);
        assert.equal(saved.action, "save");
        assert.equal(saved.body.id, "relay");
        assert.deepEqual(saved.body.directAgents, ["opencode"]);
        assert.deepEqual(errors, []);
      });

      test(`${engine} ${lang} ${width}: a ticked agent opens Advanced and is saved unticked`, async (t) => {
        const { page, errors, posts, w } = await open(t, "Ticked Relay");
        assert.equal(await adv(page).evaluate((d) => d.open), true, "Advanced opens with it ticked");
        assert.equal(await box(page, w).isChecked(), true);
        await box(page, w).uncheck();
        const saved = await save(page, posts, w);
        assert.deepEqual(saved.body.directAgents, []);
        assert.deepEqual(errors, []);
      });

      test(`${engine} ${lang} ${width}: a provider that can't be handed over, and one whose API the agent doesn't speak, say why`, async (t) => {
        {
          const { page, errors, w } = await open(t, "Proxied Relay");
          await unfold(page);
          assert.equal(await fold(page).count(), 1, "the fold is there, saying why it can't");
          assert.equal(await page.locator(".editor", { hasText: proxied.directRefusal }).count(), 1, "the reason is shown");
          assert.equal(await page.locator(".editor label.tick.direct-agent").count(), 0, "no ticks beside it");
          assert.deepEqual(errors, []);
        }
        {
          const { page, errors, posts, w } = await open(t, "Blocked Relay");
          await unfold(page);
          assert.equal(await row(page, w).isVisible(), true);
          assert.equal(await box(page, w).isDisabled(), true, "the agent can't be ticked");
          assert.equal(await page.locator(".editor", { hasText: blocked.direct[0].why }).count(), 1, "why it can't be is shown");
          const saved = await save(page, posts, w);
          assert.deepEqual(saved.body.directAgents, [], "nothing ticked is sent");
          assert.deepEqual(errors, []);
        }
        {
          const { page, errors, posts, w } = await open(t, "OpenAI");
          await unfold(page);
          assert.equal(await fold(page).count(), 0, "a preset's has no fold");
          const saved = await save(page, posts, w);
          assert.equal("directAgents" in saved.body, false, `a Save without the fold sends none: ${JSON.stringify(saved.body)}`);
          assert.deepEqual(errors, []);
        }
      });
    }
  }
}
