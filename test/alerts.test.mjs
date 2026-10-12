import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// Runs the shipped inline script of index.html with a minimal DOM stub and
// drives the attention alerts through successive /api/tree polls.
const html = readFileSync(new URL('../src/index.html', import.meta.url), 'utf8');
const script = /<script>\s*([\s\S]*?)\s*<\/script>/.exec(html);
assert.ok(script, 'inline script not found in index.html');

class El {
    constructor(name) {
        this.name = name;
        this.innerHTML = '';
        this.textContent = '';
        this.title = '';
        this.hidden = false;
        this.value = '';
        this.style = {};
        this.dataset = {};
        const cls = new Set();
        this.classList = {
            add: c => cls.add(c), remove: c => cls.delete(c),
            toggle: (c, on) => on ? cls.add(c) : cls.delete(c), has: c => cls.has(c),
        };
    }
    addEventListener() {}
    scrollIntoView() {}
    querySelector() { return null; }
    querySelectorAll() { return []; }
    getBoundingClientRect() { return {right: 0, width: 0}; }
}

function boot(first, store = {}) {
    const els = new Map();
    const el = sel => {
        if (sel.includes(' ')) return null; // descendant lookups: nothing rendered inside the stubs
        if (!els.has(sel)) els.set(sel, new El(sel));
        return els.get(sel);
    };
    let tree = first; // the page's own first load reads it: the baseline
    const played = [];
    const ctx = {
        document: {
            documentElement: new El('html'), title: '', body: new El('body'),
            querySelector: el, querySelectorAll: () => [], addEventListener() {},
        },
        fetch: async u => u === '/api/tree' ? {ok: true, json: async () => tree} : {ok: false, status: 404},
        I18N: {tr: k => k, LANGS: [], curLang: () => 'en', setLang() {}, locale: () => 'en-US'},
        localStorage: {
            getItem: k => k in store ? store[k] : null,
            setItem: (k, v) => { store[k] = String(v); },
            removeItem: k => { delete store[k]; },
        },
        Audio: class {
            constructor(src) { this.src = src; }
            play() { played.push(this.src); return Promise.resolve(); }
        },
        CSS: {escape: s => s},
        window: {addEventListener() {}},
        setInterval: () => 0,
        setTimeout: () => 0,
        clearTimeout() {},
        console,
    };
    vm.runInNewContext(script[1], ctx);
    return {
        ctx, els, played, store,
        async poll(t) {
            tree = t;
            await ctx.load();
        },
        row: () => els.get('#tree').innerHTML,
        run: code => vm.runInContext(code, ctx),
    };
}

const sess = (o = {}) => ({
    id: 's1', parent: null, title: 'fix session', agent: 'build', model: null, dir: '/p',
    maxctx: 900, in: 10, out: 20, reason: 5, cr: 7, cw: 8,
    created: 1700000000000, updated: 1700000100000, dur: 100000,
    active: true, beats: [], children: [], subtree: {in: 10, out: 20, reason: 5, cr: 7, cw: 8}, ...o,
});

const settle = () => new Promise(r => setTimeout(r, 0));

test('first poll records the baseline only: no sound, no blinking', async () => {
    const b = boot([sess({state: 'ask'})]);
    await settle();
    assert.deepEqual(b.played, []);
    assert.ok(!b.row().includes('attn'));
});

test('question, permission and done each play their own sound and blink until clicked', async () => {
    const b = boot([sess({state: 'busy'})]);
    await settle();
    await b.poll([sess({state: 'ask'})]);
    assert.deepEqual(b.played, ['/sounds/ask.oga']);
    assert.ok(b.row().includes('attn attn-ask'));

    await b.poll([sess({state: 'ask'})]);
    assert.equal(b.played.length, 1, 'an unchanged state rings once');

    await b.poll([sess({state: 'perm'})]);
    assert.equal(b.played.at(-1), '/sounds/perm.oga');
    assert.ok(b.row().includes('attn attn-perm'));

    await b.poll([sess({state: 'idle'})]);
    assert.equal(b.played.at(-1), '/sounds/done.oga');
    assert.ok(b.row().includes('attn attn-done'));

    b.ctx.dismissAttn('s1');
    assert.ok(!b.row().includes('attn'));
});

test('done waits for the whole subtree; a subagent question blinks on its collapsed parent', async () => {
    const tree = (root, kid) => [sess({state: root, children: [sess({id: 'k1', parent: 's1', state: kid})]})];
    const b = boot(tree('busy', 'busy'));
    await settle();
    await b.poll(tree('idle', 'busy'));
    assert.deepEqual(b.played, [], 'a subagent still works: not done');
    await b.poll(tree('idle', 'idle'));
    assert.deepEqual(b.played, ['/sounds/done.oga']);
    assert.ok(!b.row().includes('data-id="k1" title='), 'subagents never get a done mark');

    await b.poll(tree('busy', 'busy'));
    assert.ok(!b.row().includes('attn'), 'a new turn clears done');
    b.run("COLLAPSED.add('s1')");
    await b.poll(tree('busy', 'perm'));
    assert.match(b.row(), /class="node root[^"]* attn attn-perm" data-id="s1" title="node.attn.inside"/);
});

test('a mark goes away when the human already reacted in the terminal', async () => {
    const b = boot([sess({state: 'busy'})]);
    await settle();
    await b.poll([sess({state: 'ask'})]);
    await b.poll([sess({state: 'busy'})]);
    assert.ok(!b.row().includes('attn'));
});

test('toggles: off by choice silences sound and hides blinking; marks survive a reload', async () => {
    const b = boot([sess({state: 'busy'})], {alerts: JSON.stringify({sound: false, blink: false})});
    await settle();
    await b.poll([sess({state: 'ask'})]);
    assert.deepEqual(b.played, []);
    assert.ok(!b.row().includes('attn'));
    assert.ok(b.els.get('#sndbtn').classList.has('off'));

    const r = boot([sess({state: 'ask'})], {...b.store, alerts: JSON.stringify({sound: true, blink: true})});
    await settle();
    assert.ok(r.row().includes('attn attn-ask'), 'an unanswered question still blinks after reload');
    assert.deepEqual(r.played, []);
});
