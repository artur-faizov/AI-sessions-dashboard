import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// Same harness idea as dbfield.test.mjs: run the exact shipped inline script in a
// sandbox with a minimal DOM/fetch/localStorage stub, then exercise the provider
// dropdown flow (render, switch via POST /api/provider, stored-choice re-apply).

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
        this.handlers = {};
        this.classList = {add() {}, remove() {}, toggle() {}};
    }
    get innerHTML() { return this._html; }
    set innerHTML(v) { this._html = String(v); this.writes++; }
    addEventListener(ev, fn) { (this.handlers[ev] ||= []).push(fn); }
    dispatch(ev) { for (const fn of this.handlers[ev] || []) fn(); }
    focus() {}
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
        querySelector: el,
        querySelectorAll: () => [],
        addEventListener() {},
    };
    const rmap = new Map(Object.entries(routes));
    let active = (rmap.get('/api/provider') || {}).provider || 'opencode';
    const fetchLog = [];
    const fetch = async (u, init = {}) => {
        fetchLog.push({u, init});
        if (u === '/api/provider' && (init.method || 'GET') === 'POST') {
            const body = JSON.parse(init.body);
            const res = rmap.get('POST /api/provider') || {ok: false, error: 'no such provider'};
            if (res.ok) active = body.name;
            return {ok: !!res.ok, status: res.ok ? 200 : 400,
                    json: async () => ({...res, provider: body.name})};
        }
        if (u === '/api/provider')
            return {ok: true, json: async () => ({provider: active, providers: ['claude', 'opencode']})};
        const body = rmap.get(u);
        return body ? {ok: true, json: async () => body}
                    : {ok: false, status: 404, json: async () => ({ok: false, error: 'not found'})};
    };
    const storage = makeStorage();
    if (stored) storage.setItem('provider.name', stored);
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
        setInterval: () => 0,
        console,
        __flipProvider: v => { active = v; },
    };
    vm.runInNewContext(script[1], ctx);
    return ctx;
}

const settle = async () => {
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));
};

const provinfo = (o = {}) => ({provider: 'opencode', providers: ['claude', 'opencode'], ...o});

const sess = (o = {}) => ({
    id: 's1', parent: null, title: 'fix session', agent: 'build', model: null, dir: '/p',
    maxctx: 900, in: 10, out: 20, reason: 5, cr: 7, cw: 8,
    created: 1700000000000, updated: 1700000100000, dur: 100000,
    active: false, beats: [], children: [], subtree: {in: 10, out: 20, reason: 5, cr: 7, cw: 8}, ...o,
});

const dbinfo = (o = {}) => ({
    path: '/home/u/.local/share/opencode/opencode.db',
    found: true, error: null, candidates: [], ...o,
});

test('provider dropdown lists registry names and marks the active one', async () => {
    const ctx = boot({routes: {'/api/provider': provinfo(), '/api/tree': [sess()]}});
    await settle();
    const sel = ctx.els.get('#provsel');
    assert.ok(sel.innerHTML.includes('value="claude"'), 'claude option rendered');
    assert.ok(sel.innerHTML.includes('value="opencode"'), 'opencode option rendered');
    assert.ok(sel.innerHTML.includes('selected'), 'active provider is selected');
    assert.equal(ctx.els.get('#dbwrap').hidden, false, 'db field visible under opencode');
});

test('stored claude choice is re-applied on load and hides the db field', async () => {
    const ctx = boot({
        routes: {
            '/api/provider': provinfo(),
            'POST /api/provider': {ok: true},
            '/api/db': dbinfo(),
            '/api/tree': [sess()],
        },
        stored: 'claude',
    });
    await settle();
    const post = ctx.fetchLog.find(f => f.u === '/api/provider' && f.init.method === 'POST');
    assert.ok(post, 'stored choice POSTs to /api/provider');
    assert.equal(JSON.parse(post.init.body).name, 'claude');
    assert.equal(ctx.localStorage.getItem('provider.name'), 'claude', 'choice remembered');
    assert.equal(ctx.els.get('#dbwrap').hidden, true, 'db field hidden under claude');
});

test('changing the dropdown posts the new provider and reloads the tree', async () => {
    const ctx = boot({routes: {
        '/api/provider': provinfo(),
        'POST /api/provider': {ok: true},
        '/api/tree': [sess()],
    }});
    await settle();
    ctx.fetchLog.length = 0;
    const sel = ctx.els.get('#provsel');
    sel.value = 'claude';
    sel.dispatch('change');
    await settle();
    const post = ctx.fetchLog.find(f => f.u === '/api/provider' && f.init.method === 'POST');
    assert.ok(post, 'change POSTs to /api/provider');
    assert.equal(JSON.parse(post.init.body).name, 'claude');
    assert.equal(ctx.localStorage.getItem('provider.name'), 'claude');
    assert.ok(ctx.fetchLog.some(f => f.u === '/api/tree'), 'tree reloaded after switch');
});

test('failed switch keeps opencode active and clears the stored choice', async () => {
    const ctx = boot({routes: {
        '/api/provider': provinfo(),
        'POST /api/provider': {ok: false, error: 'directory not found: /x'},
        '/api/tree': [sess()],
    }, stored: 'claude'});
    await settle();
    const sel = ctx.els.get('#provsel');
    assert.match(sel.title, /directory not found/, 'error surfaces in the dropdown tooltip');
    assert.equal(ctx.localStorage.getItem('provider.name'), null, 'stored choice dropped');
    assert.ok(sel.innerHTML.includes('value="opencode"'), 'options re-rendered');
});

test('refresh re-applies the stored provider after a server restart resets it', async () => {
    const ctx = boot({routes: {
        '/api/provider': provinfo({provider: 'claude'}),
        'POST /api/provider': {ok: true},
        '/api/tree': [sess()],
    }, stored: 'claude'});
    await settle();
    assert.equal(ctx.fetchLog.filter(f => f.u === '/api/provider' && f.init.method === 'POST').length, 0,
        'no POST while in sync');
    ctx.__flipProvider('opencode'); // server restart resets to the startup default
    ctx.fetchLog.length = 0;
    await ctx.load();
    await settle();
    const post = ctx.fetchLog.find(f => f.u === '/api/provider' && f.init.method === 'POST');
    assert.ok(post, 'refresh re-applies the stored provider after a restart');
    assert.equal(JSON.parse(post.init.body).name, 'claude');
    assert.equal(ctx.localStorage.getItem('provider.name'), 'claude', 'choice kept');
    assert.equal(ctx.els.get('#dbwrap').hidden, true, 'db field hidden again under claude');
});

test('refresh keeps the dropdown truthful when there is no stored choice', async () => {
    const ctx = boot({routes: {
        '/api/provider': provinfo({provider: 'claude'}),
        '/api/tree': [sess()],
    }});
    await settle();
    assert.equal(ctx.fetchLog.filter(f => f.u === '/api/provider' && f.init.method === 'POST').length, 0,
        'server-started provider is not overridden');
    ctx.__flipProvider('opencode');
    ctx.fetchLog.length = 0;
    await ctx.load();
    await settle();
    assert.ok(!ctx.fetchLog.some(f => f.u === '/api/provider' && f.init.method === 'POST'),
        'no POST without a stored choice');
    const sel = ctx.els.get('#provsel');
    assert.ok(sel.innerHTML.includes('value="opencode" selected'),
        'dropdown re-rendered to the actual server provider');
    assert.equal(ctx.els.get('#dbwrap').hidden, false, 'db field visible under opencode');
});
