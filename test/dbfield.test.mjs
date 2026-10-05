import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// Same harness idea as refresh.test.mjs: run the exact shipped inline script in a
// sandbox with a minimal DOM/fetch/localStorage stub, then exercise the DB-path
// field flow (render, custom apply, stored-path auto-apply, db_not_found state).

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
    const fetchLog = [];
    const fetch = async (u, init = {}) => {
        fetchLog.push({u, init});
        if (u === '/api/db' && (init.method || 'GET') === 'POST') {
            const body = JSON.parse(init.body);
            const res = rmap.get('POST /api/db');
            return {ok: !!res.ok, status: res.ok ? 200 : 400,
                    json: async () => ({ok: !!res.ok, path: body.path, error: res.error || ''})};
        }
        const body = rmap.get(u);
        return body ? {ok: true, json: async () => body}
                    : {ok: false, status: 404, json: async () => ({ok: false, error: 'not found'})};
    };
    const storage = makeStorage();
    if (stored) storage.setItem('db.path', stored);
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
    };
    vm.runInNewContext(script[1], ctx);
    return ctx;
}

const settle = async () => {
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));
    await new Promise(r => setTimeout(r, 0));
};

const dbinfo = (o = {}) => ({
    path: '/home/u/.local/share/opencode/opencode.db',
    found: true, error: null, switchable: true,
    candidates: ['/home/u/.local/share/opencode/opencode.db', '/other/opencode.db'],
    ...o,
});

const sess = (o = {}) => ({
    id: 's1', parent: null, title: 'fix session', agent: 'build', model: null, dir: '/p',
    maxctx: 900, in: 10, out: 20, reason: 5, cr: 7, cw: 8,
    created: 1700000000000, updated: 1700000100000, dur: 100000,
    active: false, beats: [], children: [], subtree: {in: 10, out: 20, reason: 5, cr: 7, cw: 8}, ...o,
});

test('db field renders server candidates plus a Custom option, marks missing in title', async () => {
    const ctx = boot({routes: {
        '/api/db': dbinfo({found: false, error: 'file not found: /x.db'}),
        '/api/tree': [sess()],
    }});
    await settle();
    const sel = ctx.els.get('#dbsel');
    assert.ok(sel.innerHTML.includes('/home/u/.local/share/opencode/opencode.db'),
        'candidates appear as options');
    assert.ok(sel.innerHTML.includes('/other/opencode.db'), 'all candidates listed');
    assert.ok(sel.innerHTML.includes('value="__custom__"'), 'Custom… option present');
    assert.match(sel.title, /db\.missing\.tip/, 'missing state is explained in the tooltip');
});

test('db field found state shows the db title', async () => {
    const ctx = boot({routes: {'/api/db': dbinfo(), '/api/tree': [sess()]}});
    await settle();
    assert.equal(ctx.els.get('#dbsel').title, 'db.title');
});

test('picking a candidate posts the path and reloads the tree', async () => {
    const ctx = boot({routes: {
        '/api/db': dbinfo(),
        'POST /api/db': {ok: true},
        '/api/tree': [sess()],
    }});
    await settle();
    ctx.fetchLog.length = 0;
    const sel = ctx.els.get('#dbsel');
    sel.value = '/other/opencode.db';
    sel.dispatch('change');
    await settle();
    const post = ctx.fetchLog.find(f => f.u === '/api/db' && f.init.method === 'POST');
    assert.ok(post, 'selecting a candidate POSTs to /api/db');
    assert.equal(JSON.parse(post.init.body).path, '/other/opencode.db');
    assert.equal(ctx.localStorage.getItem('db.path'), '/other/opencode.db',
        'chosen path is remembered in localStorage');
    assert.ok(ctx.fetchLog.some(f => f.u === '/api/tree'),
        'tree data reloads after the switch');
});

test('Custom… reveals input + Apply; Apply posts the typed path', async () => {
    const ctx = boot({routes: {
        '/api/db': dbinfo(),
        'POST /api/db': {ok: true},
        '/api/tree': [sess()],
    }});
    await settle();
    const sel = ctx.els.get('#dbsel');
    sel.value = '__custom__';
    sel.dispatch('change');
    const inp = ctx.els.get('#dbcustom'), btn = ctx.els.get('#dbapply');
    assert.equal(inp.hidden, false, 'custom input revealed');
    assert.equal(btn.hidden, false, 'apply button revealed');
    inp.value = '/custom/my.db';
    btn.dispatch('click');
    await settle();
    const post = ctx.fetchLog.find(f => f.u === '/api/db' && f.init.method === 'POST');
    assert.equal(JSON.parse(post.init.body).path, '/custom/my.db');
    assert.equal(ctx.localStorage.getItem('db.path'), '/custom/my.db');
});

test('failed apply keeps the field marked missing with the server error', async () => {
    const ctx = boot({routes: {
        '/api/db': dbinfo(),
        'POST /api/db': {ok: false, error: 'nope'},
        '/api/tree': [sess()],
    }});
    await settle();
    const btn = ctx.els.get('#dbapply');
    ctx.els.get('#dbcustom').value = '/bad.db';
    btn.dispatch('click');
    await settle();
    assert.equal(ctx.els.get('#dbsel').title, 'nope', 'server error lands in the tooltip');
    assert.equal(ctx.localStorage.getItem('db.path'), null, 'failed path is not stored');
});

test('stored path from a previous run auto-applies on load', async () => {
    const ctx = boot({
        routes: {
            '/api/db': dbinfo(),
            'POST /api/db': {ok: true},
            '/api/tree': [sess()],
        },
        stored: '/remembered/my.db',
    });
    await settle();
    const post = ctx.fetchLog.find(f => f.u === '/api/db' && f.init.method === 'POST');
    assert.ok(post, 'stored path is re-applied at startup');
    assert.equal(JSON.parse(post.init.body).path, '/remembered/my.db');
});

test('stale stored path is dropped, server autodetect stays in charge', async () => {
    const ctx = boot({
        routes: {
            '/api/db': dbinfo(),
            'POST /api/db': {ok: false, error: 'file not found'},
            '/api/tree': [sess()],
        },
        stored: '/gone/my.db',
    });
    await settle();
    assert.equal(ctx.localStorage.getItem('db.path'), null, 'stale stored path is cleared');
    assert.equal(ctx.els.get('#dbsel').title, 'db.title',
        'field keeps the server autodetected state, not red');
});

test('db_not_found on /api/tree renders a localized empty state and refreshes db info', async () => {
    const ctx = boot({routes: {
        '/api/db': dbinfo({found: false, error: 'file not found: /x.db'}),
        '/api/tree': {error: 'db_not_found', path: '/x.db'},
    }});
    await settle();
    assert.ok(ctx.els.get('#tree').innerHTML.includes('db.notfound'),
        'tree area shows the db-not-found empty state');
    assert.equal(ctx.els.get('#summary').textContent, '');
    const sel = ctx.els.get('#dbsel');
    assert.match(sel.title, /db\.missing\.tip/, 'field reflects missing state');
});