import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// Same harness as dbfield.test.mjs: run the exact shipped inline script in a
// sandbox with a minimal DOM/fetch/localStorage stub, then check the merged
// view: every provider's sessions in one tree, each root marked with its
// provider's icon, and no provider switcher.

const html = readFileSync(new URL('../src/index.html', import.meta.url), 'utf8');
const script = /<script>\s*([\s\S]*?)\s*<\/script>/.exec(html);
assert.ok(script, 'inline script not found in index.html');

class El {
    constructor(name) {
        this.name = name;
        this._html = '';
        this.writes = 0;
        this.textContent = '';
        this.title = '';
        this.hidden = false;
        this.value = '';
        this.placeholder = '';
        this.style = {setProperty() {}};
        this.dataset = {};
        this.handlers = {};
        this.classList = {add() {}, remove() {}, toggle() {}};
    }
    get innerHTML() { return this._html; }
    set innerHTML(v) { this._html = String(v); this.writes++; }
    addEventListener(ev, fn) { (this.handlers[ev] ||= []).push(fn); }
    dispatch(ev) { for (const fn of this.handlers[ev] || []) fn(); }
    focus() {}
    scrollIntoView() {}
    querySelector() { return null; }
    querySelectorAll() { return []; }
    getBoundingClientRect() { return {right: 0, width: 0}; }
}

function makeStorage() {
    const m = new Map();
    return {
        getItem: k => (m.has(k) ? m.get(k) : null),
        setItem: (k, v) => m.set(k, String(v)),
        removeItem: k => m.delete(k),
    };
}

function boot({routes = {}, stored = null} = {}) {
    const els = new Map();
    const el = sel => {
        if (!els.has(sel)) els.set(sel, new El(sel));
        return els.get(sel);
    };
    const document = {
        documentElement: new El('html'),
        title: '',
        body: new El('body'),
        querySelector: sel => sel === '#tree .rninput' ? null : el(sel), // no rename in progress
        querySelectorAll: () => [],
        addEventListener() {},
    };
    const rmap = new Map(Object.entries(routes));
    const fetchLog = [];
    const fetch = async (u, init = {}) => {
        fetchLog.push({u, init});
        if (u === '/api/db' && (init.method || 'GET') === 'POST') {
            const body = JSON.parse(init.body);
            const res = rmap.get('POST /api/db');
            return {ok: !!res.ok, status: res.ok ? 200 : 400,
                    json: async () => ({ok: !!res.ok, provider: body.provider, path: body.path, error: res.error || ''})};
        }
        const body = rmap.get(u);
        return body ? {ok: true, json: async () => body}
                    : {ok: false, status: 404, json: async () => ({ok: false, error: 'not found'})};
    };
    const storage = makeStorage();
    for (const [k, v] of Object.entries(stored || {})) storage.setItem(k, v);
    const I18N = {
        tr: k => k,
        LANGS: [],
        curLang: () => 'en',
        setLang() {},
        locale: () => 'en-US',
    };
    const ctx = {
        document, fetch, I18N, els, routes: rmap, fetchLog, localStorage: storage,
        window: {addEventListener() {}},
        CSS: {escape: s => s},
        setInterval: () => 0,
        setTimeout,
        clearTimeout,
        console,
    };
    vm.runInNewContext(script[1], ctx);
    return ctx;
}

const settle = async () => {
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));
};

const sess = (prov, raw, o = {}) => ({
    id: prov + ':' + raw, parent: null, title: prov + ' session', agent: 'build', model: null, dir: '/p',
    maxctx: 900, in: 10, out: 20, reason: 5, cr: 7, cw: 8,
    created: Date.now() - 60000, updated: Date.now(), dur: 100000, prov,
    active: false, beats: [], children: [], subtree: {in: 10, out: 20, reason: 5, cr: 7, cw: 8}, ...o,
});

test('roots of every provider share one tree, each with its provider icon', async () => {
    const kid = sess('claude', 'k1', {parent: 'claude:s1', title: 'kid'});
    const ctx = boot({routes: {
        '/api/db': {sources: []},
        '/api/tree': [sess('claude', 's1', {children: [kid]}), sess('codex', 's1'), sess('gemini', 'g1')],
    }});
    await settle();
    const html = ctx.els.get('#tree').innerHTML;
    assert.ok(html.includes('data-id="claude:s1"') && html.includes('data-id="codex:s1"'), 'both roots listed');
    assert.ok(html.includes('src="/icons/claude.svg"') && html.includes('src="/icons/codex.svg"'), 'roots carry their icon');
    assert.equal(html.split('class="provic').length - 1, 3, 'one icon per root, none on subagents');
    assert.match(html, /provltr" title="gemini">g</, 'a provider without an icon gets a letter badge');
    assert.equal(html.split('class="project"').length - 1, 1, 'same directory groups across providers');
});

test('the page never asks for or switches a provider', async () => {
    const ctx = boot({routes: {'/api/db': {sources: []}, '/api/tree': [sess('claude', 's1')]}});
    await settle();
    assert.ok(!ctx.fetchLog.some(f => f.u.startsWith('/api/provider')), 'no /api/provider calls');
    assert.equal(ctx.els.has('#provsel'), false, 'no provider dropdown is touched');
});

test('favourites and names stored under bare ids move to the qualified ids', async () => {
    const ctx = boot({
        routes: {'/api/db': {sources: []}, '/api/tree': [sess('claude', 's1'), sess('codex', 'x9')]},
        stored: {'ttree-settings': JSON.stringify({v: 2, chk: {only: false, marked: ['s1', 'gone']}, names: {x9: 'mine'}})},
    });
    await settle();
    const saved = JSON.parse(ctx.localStorage.getItem('ttree-settings'));
    assert.deepEqual(saved.chk.marked.sort(), ['claude:s1', 'gone']);
    assert.deepEqual(saved.names, {'codex:x9': 'mine'});
    assert.ok(ctx.els.get('#tree').innerHTML.includes('mine'), 'the local name shows');
});
