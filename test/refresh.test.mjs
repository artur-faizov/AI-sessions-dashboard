import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// The whole UI is one inline script inside index.html. This harness runs the
// exact shipped script in a sandbox with a minimal DOM/fetch stub, so these
// tests exercise the real refresh flow (load/pick) end to end.
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
        this.lang = '';
        this.style = {};
        this.dataset = {};
        this.classList = {add() {}, remove() {}, toggle() {}};
    }
    get innerHTML() { return this._html; }
    set innerHTML(v) { this._html = String(v); this.writes++; }
    addEventListener() {}
    querySelector() { return null; }
    querySelectorAll() { return []; }
    getBoundingClientRect() { return {right: 0, width: 0}; }
}

function boot(routes = {}) {
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
    const fetch = async u => {
        fetchLog.push(u);
        const body = rmap.get(u);
        return body ? {ok: true, json: async () => body} : {ok: false, status: 404};
    };
    const I18N = {
        tr: k => k,
        LANGS: [],
        curLang: () => 'en',
        setLang() {},
        locale: () => 'en-US',
    };
    const ctx = {
        document, fetch, I18N, fetchLog, els,
        routes: rmap,
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
};

const sess = (o = {}) => ({
    id: 's1', parent: null, title: 'fix session', agent: 'build', model: null, dir: '/p',
    maxctx: 900, in: 10, out: 20, reason: 5, cr: 7, cw: 8,
    created: 1700000000000, updated: 1700000100000, dur: 100000,
    active: false, beats: [], children: [], subtree: {in: 10, out: 20, reason: 5, cr: 7, cw: 8}, ...o,
});
const call = (o = {}) => ({mid: 'm', time: 1700000000000, in: 1, cr: 0, cw: 0, out: 0, reason: 0, ctx: 111, ...o});

test('refresh re-fetches AI calls of the selected session', async () => {
    const ctx = boot({
        '/api/tree': [sess()],
        '/api/prompt/s1': {texts: []},
        '/api/calls/s1': {calls: [call({mid: 'm1', ctx: 111})]},
    });
    await settle();
    await ctx.pick('s1');
    await settle();
    assert.ok(ctx.els.get('#calls').innerHTML.includes('111'), 'picked session renders its calls');

    // new data arrives on the server; refresh must pick it up
    ctx.routes.set('/api/calls/s1', {calls: [call({mid: 'm1', ctx: 111}), call({mid: 'm2', ctx: 777})]});
    ctx.fetchLog.length = 0;
    await ctx.load();
    await settle();

    assert.ok(ctx.fetchLog.includes('/api/calls/s1'),
        'refresh must re-fetch the AI calls of the selected session');
    assert.ok(ctx.els.get('#calls').innerHTML.includes('777'),
        'refreshed calls table must show the new call');
});

test('refresh updates the detail panel in place, not rebuilding it', async () => {
    const ctx = boot({
        '/api/tree': [sess()],
        '/api/prompt/s1': {texts: []},
        '/api/calls/s1': {calls: [call({mid: 'm1', ctx: 111})]},
    });
    await settle();
    await ctx.pick('s1');
    await settle();
    const detailWrites = ctx.els.get('#detail').writes;

    await ctx.load();
    await settle();

    assert.equal(ctx.els.get('#detail').writes, detailWrites,
        '#detail must not be rewritten on refresh: it is the scroll container, a rewrite resets scroll and flashes loading');
});

test('tree row shows total(own) for nodes with subagents, and refresh re-renders it', async () => {
    const child = sess({id: 'c1', parent: 's1', title: 'sub', in: 500, out: 0, reason: 0, cr: 0, cw: 0,
        subtree: {in: 500, out: 0, reason: 0, cr: 0, cw: 0}});
    const parent = (own) => sess({in: own, out: 0, reason: 0, cr: 0, cw: 0, children: [child],
        subtree: {in: own + 500, out: 0, reason: 0, cr: 0, cw: 0}});
    const ctx = boot({'/api/tree': [parent(1000)]});
    await settle();
    await ctx.load();
    await settle();
    assert.ok(ctx.document.querySelector('#tree').innerHTML.includes('<b>1.5k</b><span class="own-part">(1.0k)</span>'),
        'spent cell must show total(own) for the parent');

    ctx.routes.set('/api/tree', [parent(4200)]);
    await ctx.load();
    await settle();

    assert.ok(ctx.document.querySelector('#tree').innerHTML.includes('<b>4.7k</b><span class="own-part">(4.2k)</span>'),
        'total(own) must be re-rendered from the fresh tree');
});

test('refresh updates the session prompt text', async () => {
    const ctx = boot({
        '/api/tree': [sess()],
        '/api/prompt/s1': {texts: ['first prompt']},
        '/api/calls/s1': {calls: [call({mid: 'm1', ctx: 111})]},
    });
    await settle();
    await ctx.pick('s1');
    await settle();
    assert.equal(ctx.els.get('#pp').textContent, 'first prompt');

    ctx.routes.set('/api/prompt/s1', {texts: ['first prompt', 'second prompt']});
    await ctx.load();
    await settle();

    assert.equal(ctx.els.get('#pp').textContent, 'first prompt\n\n—\n\nsecond prompt');
});

test('refresh does not fetch calls or prompt when no session is selected', async () => {
    const ctx = boot({
        '/api/tree': [sess()],
        '/api/prompt/s1': {texts: []},
        '/api/calls/s1': {calls: [call({mid: 'm1', ctx: 111})]},
    });
    await settle();
    ctx.fetchLog.length = 0;
    await ctx.load();
    await settle();
    assert.ok(!ctx.fetchLog.some(u => u.startsWith('/api/calls')));
    assert.ok(!ctx.fetchLog.some(u => u.startsWith('/api/prompt')));
});

test('tree hearts: every active session gets the same background ecg, inactive none', async () => {
    const ctx = boot({
        '/api/tree': [
            sess({id: 'top', active: true, updated: 9000000}),
            sess({id: 'second', active: true, updated: 8000000}),
            sess({id: 'idle'}),
        ],
    });
    await settle();
    const tree = ctx.els.get('#tree').innerHTML;
    assert.equal((tree.match(/<svg class="ttlbg"/g) || []).length, 2, 'one background ecg per active session');
    assert.ok(!/class="ecg (xl|lg)"/.test(tree), 'no size variants');
});

test('recent-only filter keeps active sessions and their parent chain', async () => {
    const ctx = boot({
        '/api/tree': [
            sess({id: 'p', children: [sess({id: 'k', active: true, updated: 9000000})]}),
            sess({id: 'alone'}),
        ],
    });
    await settle();
    vm.runInNewContext('REC.only = true; render();', ctx);
    const tree = ctx.els.get('#tree').innerHTML;
    assert.ok(tree.includes('data-id="k"'), 'active child stays visible');
    assert.ok(tree.includes('data-id="p"'), 'parent of an active child stays visible');
    assert.ok(!tree.includes('data-id="alone"'), 'inactive standalone session is dropped');
});

test('agent badge column is gone for every agent', async () => {
    const ctx = boot({
        '/api/tree': [
            sess({id: 'r1', agent: 'build'}),
            sess({id: 'r2', agent: 'plan', children: [sess({id: 'ke', agent: 'explore'})]}),
        ],
    });
    await settle();
    const tree = ctx.els.get('#tree').innerHTML;
    assert.ok(!tree.includes('badge agent'), 'no agent badge rendered');
});

test('favourite star only on roots, filled when marked', async () => {
    const ctx = boot({
        '/api/tree': [sess({id: 'r1', children: [sess({id: 'k1'})]})],
    });
    await settle();
    vm.runInNewContext("CHK.marked.add('r1'); render();", ctx);
    const tree = ctx.els.get('#tree').innerHTML;
    assert.equal((tree.match(/class="star/g) || []).length, 1, 'subagent has no star');
    assert.ok(/class="star on"[^>]*>★</.test(tree), 'marked root shows filled star');
});

test('favourites filter shows subagents spawned after the root was starred', async () => {
    const ctx = boot({
        '/api/tree': [sess({id: 'r1', children: [sess({id: 'k1', children: [sess({id: 'g1'})]})]}), sess({id: 'r2'})],
    });
    await settle();
    vm.runInNewContext("CHK.marked.add('r1'); CHK.only = true; render();", ctx);
    const tree = ctx.els.get('#tree').innerHTML;
    assert.ok(tree.includes('data-id="k1"'), 'new subagent of a favourite is visible');
    assert.ok(tree.includes('data-id="g1"'), 'nested new subagent is visible');
    assert.ok(!tree.includes('data-id="r2"'), 'non-favourite root is dropped');
});

test('active sessions render an ECG sparkline across the heart', async () => {
    const now = Date.now();
    const ctx = boot({
        '/api/tree': [sess({id: 'beat', active: true, updated: now, beats: [[now - 600000, now - 590000], [now - 120000, 0]]})],
    });
    await settle();
    const tree = ctx.els.get('#tree').innerHTML;
    assert.ok(/<svg class="ttlbg"/.test(tree), 'active session renders a background ECG svg');
    assert.ok(/<polyline points="[^"]+"/.test(tree), 'sparkline polyline with points rendered');
});

test('refresh tolerates the selected session vanishing from the tree', async () => {
    const ctx = boot({
        '/api/tree': [sess()],
        '/api/prompt/s1': {texts: []},
        '/api/calls/s1': {calls: [call({mid: 'm1', ctx: 111})]},
    });
    await settle();
    await ctx.pick('s1');
    await settle();
    assert.ok(ctx.els.get('#calls').innerHTML.includes('111'));

    ctx.routes.set('/api/tree', []);
    ctx.fetchLog.length = 0;
    await ctx.load();
    await settle();

    assert.ok(!ctx.fetchLog.some(u => u.startsWith('/api/calls')),
        'no calls fetch for a session that no longer exists');
    assert.ok(ctx.els.get('#calls').innerHTML.includes('111'),
        'previously rendered calls stay visible');
});
