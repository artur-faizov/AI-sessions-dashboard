import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// Same harness idea as refresh.test.mjs: run the exact shipped inline script in a
// sandbox with a minimal DOM/fetch/localStorage stub, then exercise the settings
// "data sources" rows (one per provider: path with default, edit / reset, on/off,
// stored choices re-applied, db_not_found state).

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
        querySelector: sel => sel === '#tree .rninput' ? null : el(sel),
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
                    json: async () => ({ok: !!res.ok, provider: body.provider, path: body.path, enabled: body.enabled, error: res.error || ''})};
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

const src = (provider, o = {}) => ({
    provider, path: '/home/u/.' + provider, default: '/home/u/.' + provider,
    found: true, error: null, candidates: ['/home/u/.' + provider], switchable: true, enabled: true, ...o,
});
const oc = (o = {}) => src('opencode', {path: '/home/u/oc/opencode.db', default: '/home/u/oc/opencode.db', ...o});
const dbinfo = (o = {}, ...more) => ({sources: [oc(o), ...more]});

const sess = (o = {}) => ({
    id: 'opencode:s1', parent: null, title: 'fix session', agent: 'build', model: null, dir: '/p',
    maxctx: 900, in: 10, out: 20, reason: 5, cr: 7, cw: 8,
    created: Date.now() - 60000, updated: Date.now(), dur: 100000, prov: 'opencode',
    active: false, beats: [], children: [], subtree: {in: 10, out: 20, reason: 5, cr: 7, cw: 8}, ...o,
});

const posts = ctx => ctx.fetchLog.filter(f => f.u === '/api/db' && f.init.method === 'POST').map(f => JSON.parse(f.init.body));

test('one row per provider in the settings panel, with icon, name and path', async () => {
    const ctx = boot({routes: {
        '/api/db': dbinfo({}, src('claude'), src('codex'), src('copilot', {found: false, error: 'directory not found: x'}),
            src('folder', {path: '', default: '', found: false, error: 'no folder selected', candidates: []})),
        '/api/tree': [sess(), sess({id: 'claude:a', prov: 'claude'}), sess({id: 'claude:b', prov: 'claude'})],
    }});
    await settle();
    const box = ctx.els.get('#dbsrcs').innerHTML;
    for (const p of ['opencode', 'claude', 'codex', 'copilot', 'folder']) {
        assert.ok(box.includes(`id="dbwrap-${p}"`), 'a row for ' + p);
        assert.ok(box.includes(`/icons/${p}.svg`), 'icon for ' + p);
        assert.ok(box.includes(`id="dbon-${p}"`) && box.includes(`id="dbreset-${p}"`), 'on/off and reset for ' + p);
    }
    assert.ok(box.includes('prov.claude'), 'provider names shown');
    assert.equal(ctx.els.get('#dbpath-claude').textContent, '/home/u/.claude · src.default');
    assert.equal(ctx.els.get('#dbpath-claude').className, 'dbpath dflt', 'the default path is dimmed');
    assert.equal(ctx.els.get('#dbreset-claude').hidden, true, 'nothing to reset at the default');
    assert.equal(ctx.els.get('#dbstat-copilot').textContent, 'src.missing');
    assert.equal(ctx.els.get('#dbstat-copilot').title, 'directory not found: x');
    assert.equal(ctx.els.get('#dbstat-claude').textContent, 'src.found', 'found state (tr stub drops the count)');
    assert.equal(ctx.els.get('#dbpath-folder').textContent, 'src.none');
    assert.equal(ctx.els.get('#dbon-claude').checked, true);
});

test('an overridden path is shown plainly with a reset button', async () => {
    const ctx = boot({routes: {'/api/db': dbinfo({path: '/x/opencode.db'}), '/api/tree': [sess()]}});
    await settle();
    assert.equal(ctx.els.get('#dbpath-opencode').textContent, '/x/opencode.db');
    assert.equal(ctx.els.get('#dbpath-opencode').className, 'dbpath');
    assert.equal(ctx.els.get('#dbreset-opencode').hidden, false);
});

test('edit reveals input + Apply; Apply posts the provider and path and reloads the tree', async () => {
    const ctx = boot({routes: {'/api/db': dbinfo({}, src('codex')), 'POST /api/db': {ok: true}, '/api/tree': [sess()]}});
    await settle();
    ctx.fetchLog.length = 0;
    const inp = ctx.els.get('#dbcustom-codex'), btn = ctx.els.get('#dbapply-codex');
    inp.hidden = btn.hidden = true; // the stub does not parse the hidden attribute
    ctx.els.get('#dbedit-codex').dispatch('click');
    assert.equal(inp.hidden, false, 'custom input revealed');
    assert.equal(btn.hidden, false, 'apply button revealed');
    assert.equal(inp.value, '/home/u/.codex', 'starts from the current path');
    inp.value = '/other/codex';
    btn.dispatch('click');
    await settle();
    assert.deepEqual(posts(ctx), [{provider: 'codex', path: '/other/codex'}]);
    assert.equal(ctx.localStorage.getItem('db.path.codex'), '/other/codex', 'remembered per provider');
    assert.equal(ctx.localStorage.getItem('db.path'), null, 'the opencode path is untouched');
    assert.ok(ctx.fetchLog.some(f => f.u === '/api/tree'), 'tree data reloads after the switch');
});

test('the opencode path keeps its historic storage key', async () => {
    const ctx = boot({routes: {'/api/db': dbinfo(), 'POST /api/db': {ok: true}, '/api/tree': [sess()]}});
    await settle();
    ctx.els.get('#dbcustom-opencode').value = '/custom/my.db';
    ctx.els.get('#dbapply-opencode').dispatch('click');
    await settle();
    assert.equal(ctx.localStorage.getItem('db.path'), '/custom/my.db');
});

test('reset posts reset and forgets the stored path', async () => {
    const ctx = boot({
        routes: {'/api/db': dbinfo({}, src('claude', {path: '/mine'})), 'POST /api/db': {ok: true}, '/api/tree': [sess()]},
        stored: {'db.path.claude': '/mine'},
    });
    await settle();
    ctx.fetchLog.length = 0;
    ctx.els.get('#dbreset-claude').dispatch('click');
    await settle();
    assert.deepEqual(posts(ctx), [{provider: 'claude', reset: true}]);
    assert.equal(ctx.localStorage.getItem('db.path.claude'), null);
});

test('the on/off checkbox posts enabled and is remembered', async () => {
    const ctx = boot({routes: {'/api/db': dbinfo({}, src('codex')), 'POST /api/db': {ok: true}, '/api/tree': [sess()]}});
    await settle();
    ctx.fetchLog.length = 0;
    const on = ctx.els.get('#dbon-codex');
    on.checked = false;
    on.dispatch('change');
    await settle();
    assert.deepEqual(posts(ctx), [{provider: 'codex', enabled: false}]);
    assert.equal(ctx.localStorage.getItem('db.off'), '["codex"]');
    on.checked = true;
    on.dispatch('change');
    await settle();
    assert.equal(ctx.localStorage.getItem('db.off'), null);
});

test('failed apply marks the row with the server error', async () => {
    const ctx = boot({routes: {'/api/db': dbinfo(), 'POST /api/db': {ok: false, error: 'nope'}, '/api/tree': [sess()]}});
    await settle();
    ctx.els.get('#dbcustom-opencode').value = '/bad.db';
    ctx.els.get('#dbapply-opencode').dispatch('click');
    await settle();
    assert.equal(ctx.els.get('#dbstat-opencode').textContent, 'nope', 'server error shown in the status');
    assert.equal(ctx.localStorage.getItem('db.path'), null, 'failed path is not stored');
});

test('stored choices from a previous run auto-apply on load, per provider', async () => {
    const ctx = boot({
        routes: {'/api/db': dbinfo({}, src('folder', {path: '', default: ''}), src('codex')), 'POST /api/db': {ok: true}, '/api/tree': [sess()]},
        stored: {'db.path': '/remembered/my.db', 'db.path.folder': '/remembered/exports', 'db.off': '["codex"]'},
    });
    await settle();
    assert.deepEqual(posts(ctx), [
        {provider: 'opencode', path: '/remembered/my.db'},
        {provider: 'folder', path: '/remembered/exports'},
        {provider: 'codex', enabled: false},
    ]);
});

test('stale stored path is dropped, server autodetect stays in charge', async () => {
    const ctx = boot({
        routes: {'/api/db': dbinfo(), 'POST /api/db': {ok: false, error: 'file not found'}, '/api/tree': [sess()]},
        stored: {'db.path': '/gone/my.db'},
    });
    await settle();
    assert.equal(ctx.localStorage.getItem('db.path'), null, 'stale stored path is cleared');
    assert.equal(ctx.els.get('#dbstat-opencode').className, 'dbstat ok', 'row keeps the server state, not red');
});

test('db_not_found on /api/tree renders a localized empty state and refreshes db info', async () => {
    const ctx = boot({routes: {
        '/api/db': dbinfo({found: false, error: 'file not found: /x.db'}),
        '/api/tree': {error: 'db_not_found'},
    }});
    await settle();
    assert.ok(ctx.els.get('#tree').innerHTML.includes('db.notfound'),
        'tree area shows the db-not-found empty state');
    assert.equal(ctx.els.get('#statrows').textContent, 'stats.rows',
        'stats area shows an empty state');
    assert.equal(ctx.els.get('#dbstat-opencode').textContent, 'src.missing', 'row reflects missing state');
});
