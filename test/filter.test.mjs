import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// The pure filter logic lives inside index.html between marker comments.
// This harness extracts that block and runs it in a sandbox, so the tests
// always exercise the exact code shipped to the browser.
const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8');
const block = /\/\/\s*FILTER-LOGIC-START([\s\S]*?)\/\/\s*FILTER-LOGIC-END/.exec(html);
assert.ok(block, 'FILTER-LOGIC block not found in index.html');
const mod = { exports: {} };
vm.runInNewContext(block[1], { module: mod, console });
const L = mod.exports;

const node = (o = {}) => ({
    id: 'n', parent: null, title: '(untitled)', agent: '', model: null, dir: '',
    maxctx: 0, in: 0, out: 0, reason: 0, cr: 0, cw: 0, created: 0, updated: 0, dur: 0,
    children: [], subtree: { in: 0, out: 0, reason: 0, cr: 0, cw: 0 }, ...o,
});
const tf = (o = {}) => ({
    text: '', models: new Set(), providers: new Set(),
    ctxMin: null, ctxMax: null, spentMin: null, spentMax: null, durMin: null, durMax: null, ...o,
});
const cf = (o = {}) => ({
    text: '', numMin: null, numMax: null, ctxMin: null, ctxMax: null, deltaMin: null, deltaMax: null,
    outMin: null, outMax: null, reasonMin: null, reasonMax: null, inMin: null, inMax: null,
    crMin: null, crMax: null, ...o,
});
const call = (o = {}) => ({ mid: 'm', time: 0, in: 0, cr: 0, cw: 0, out: 0, reason: 0, ctx: 0, ...o });

test('inRange: null bounds pass, bounds inclusive', () => {
    assert.equal(L.inRange(5, null, null), true);
    assert.equal(L.inRange(5, 5, null), true);
    assert.equal(L.inRange(5, null, 5), true);
    assert.equal(L.inRange(5, 6, null), false);
    assert.equal(L.inRange(5, null, 4), false);
});

test('hmsToMs: empty -> null, mixed h/m/s -> milliseconds', () => {
    assert.equal(L.hmsToMs('', '', ''), null);
    assert.equal(L.hmsToMs(), null);
    assert.equal(L.hmsToMs('1', '30', '0'), 5400000);
    assert.equal(L.hmsToMs('', '', '90'), 90000);
    assert.equal(L.hmsToMs('0', '2', ''), 120000);
});

test('modelName: object modelID/id, plain string, empty -> em dash', () => {
    assert.equal(L.modelName({ modelID: 'gpt-5' }), 'gpt-5');
    assert.equal(L.modelName({ id: 'm2' }), 'm2');
    assert.equal(L.modelName('claude-x'), 'claude-x');
    assert.equal(L.modelName(null), '—');
    assert.equal(L.modelName(undefined), '—');
});

test('tree text filter: case-insensitive over title, agent, model; empty = off', () => {
    const n = node({ title: 'Build API', agent: 'Coder', model: { modelID: 'gpt-5' } });
    assert.equal(L.treeMatchesNode(n, tf()), true);
    assert.equal(L.treeMatchesNode(n, tf({ text: 'build' })), true);
    assert.equal(L.treeMatchesNode(n, tf({ text: 'CODER' })), true);
    assert.equal(L.treeMatchesNode(n, tf({ text: 'GPT-5' })), true);
    assert.equal(L.treeMatchesNode(n, tf({ text: 'zzz' })), false);
    assert.equal(L.treeMatchesNode(node({ model: 'claude-x' }), tf({ text: 'claude' })), true);
});

test('tree model filter: empty set = off, membership by model name', () => {
    const n = node({ model: { modelID: 'gpt-5' } });
    const s = node({ model: 'claude-x' });
    assert.equal(L.treeMatchesNode(n, tf()), true);
    assert.equal(L.treeMatchesNode(n, tf({ models: new Set(['gpt-5']) })), true);
    assert.equal(L.treeMatchesNode(n, tf({ models: new Set(['claude-x']) })), false);
    assert.equal(L.treeMatchesNode(s, tf({ models: new Set(['claude-x']) })), true);
});

test('tree provider filter: providerID from object model, string models are (none)', () => {
    const n = node({ model: { providerID: 'anthropic', modelID: 'claude' } });
    const s = node({ model: 'claude-x' });
    assert.equal(L.nodeProvider(n), 'anthropic');
    assert.equal(L.nodeProvider(s), '(none)');
    assert.equal(L.treeMatchesNode(n, tf({ providers: new Set(['anthropic']) })), true);
    assert.equal(L.treeMatchesNode(n, tf({ providers: new Set(['openai']) })), false);
    assert.equal(L.treeMatchesNode(s, tf({ providers: new Set(['(none)']) })), true);
});

test('tree ctx filter: min/max in KILO tokens, inclusive (500 = 500k)', () => {
    const n = node({ maxctx: 500000 });
    assert.equal(L.treeMatchesNode(n, tf({ ctxMin: 500 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ ctxMin: 501 })), false);
    assert.equal(L.treeMatchesNode(n, tf({ ctxMax: 500 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ ctxMax: 499 })), false);
});

test('tree spent filter: min/max in KILO tokens, inclusive (1750 = 1750k)', () => {
    const n = node({ subtree: { in: 1000000, out: 500000, reason: 250000, cr: 0, cw: 0 } });
    assert.equal(L.treeMatchesNode(n, tf({ spentMin: 1750 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ spentMin: 1751 })), false);
    assert.equal(L.treeMatchesNode(n, tf({ spentMax: 1750 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ spentMax: 1749 })), false);
});

test('tree kilo interpretation: 600 filters at 600000', () => {
    assert.equal(L.treeMatchesNode(node({ maxctx: 600000 }), tf({ ctxMin: 600 })), true);
    assert.equal(L.treeMatchesNode(node({ maxctx: 599999 }), tf({ ctxMin: 600 })), false);
});

test('tree duration filter: min/max on node dur (ms)', () => {
    const n = node({ dur: 65000 });
    assert.equal(L.treeMatchesNode(n, tf({ durMin: 65000 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ durMin: 65001 })), false);
    assert.equal(L.treeMatchesNode(n, tf({ durMax: 65000 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ durMax: 64999 })), false);
});

test('tree node must satisfy ALL active criteria (AND)', () => {
    const n = node({
        title: 'fix bug', model: { providerID: 'openai', modelID: 'gpt-5' },
        maxctx: 10000, subtree: { in: 100, out: 100, reason: 100, cr: 0, cw: 0 },
    });
    const f = tf({ text: 'bug', models: new Set(['gpt-5']), ctxMin: 5, spentMax: 0.3 });
    assert.equal(L.treeMatchesNode(n, f), true);
    assert.equal(L.treeMatchesNode(n, tf({ ...f, models: new Set(['other']) })), false);
    assert.equal(L.treeMatchesNode(n, tf({ ...f, ctxMin: 20 })), false);
    assert.equal(L.treeMatchesNode(n, tf({ ...f, text: 'other' })), false);
});

test('treeVisible: root shown if it or any descendant matches', () => {
    const root = node({
        title: 'plain',
        children: [node({ title: 'other' }), node({ title: 'mid', children: [node({ title: 'gold nugget' })] })],
    });
    assert.equal(L.treeVisible(root, tf({ text: 'gold' })), true);
    assert.equal(L.treeVisible(root, tf({ text: 'plain' })), true);
    assert.equal(L.treeVisible(root, tf({ text: 'nothing' })), false);
});

test('isHotCtx: strictly above kilo threshold, null threshold = off', () => {
    assert.equal(L.isHotCtx(100001, 100), true);
    assert.equal(L.isHotCtx(100000, 100), false);
    assert.equal(L.isHotCtx(999999, null), false);
    assert.equal(L.isHotCtx(500, 0.3), true);
    assert.equal(L.isHotCtx(300, 0.3), false);
});

test('treeHotVisible: node or any descendant above threshold', () => {
    const root = node({ maxctx: 50000, children: [node({ maxctx: 150000 })] });
    assert.equal(L.treeHotVisible(root, 100), true);
    assert.equal(L.treeHotVisible(node({ maxctx: 50000 }), 100), false);
    assert.equal(L.treeHotVisible(node({ maxctx: 150000 }), 100), true);
});

test('callDeltaAt: first call delta = its ctx, later = growth vs previous', () => {
    const calls = [call({ ctx: 100 }), call({ ctx: 150 }), call({ ctx: 120 })];
    assert.equal(L.callDeltaAt(calls, 0), 100);
    assert.equal(L.callDeltaAt(calls, 1), 50);
    assert.equal(L.callDeltaAt(calls, 2), -30);
});

test('callRowMatches: min/max on every column', () => {
    const row = { n: 3, ctx: 1000, delta: 200, out: 50, reason: 30, in: 120, cr: 800, text: '10/5/2026, 3:24:15 PM' };
    assert.equal(L.callRowMatches(row, cf()), true);
    assert.equal(L.callRowMatches(row, cf({ numMin: 3, numMax: 3 })), true);
    assert.equal(L.callRowMatches(row, cf({ numMin: 4 })), false);
    assert.equal(L.callRowMatches(row, cf({ ctxMin: 1000, ctxMax: 1000 })), true);
    assert.equal(L.callRowMatches(row, cf({ ctxMax: 999 })), false);
    assert.equal(L.callRowMatches(row, cf({ deltaMin: 200 })), true);
    assert.equal(L.callRowMatches(row, cf({ deltaMax: 199 })), false);
    assert.equal(L.callRowMatches(row, cf({ outMin: 50 })), true);
    assert.equal(L.callRowMatches(row, cf({ outMax: 49 })), false);
    assert.equal(L.callRowMatches(row, cf({ reasonMin: 30 })), true);
    assert.equal(L.callRowMatches(row, cf({ reasonMax: 29 })), false);
    assert.equal(L.callRowMatches(row, cf({ inMin: 120 })), true);
    assert.equal(L.callRowMatches(row, cf({ inMax: 119 })), false);
    assert.equal(L.callRowMatches(row, cf({ crMin: 800 })), true);
    assert.equal(L.callRowMatches(row, cf({ crMax: 799 })), false);
});

test('callRowMatches: text on row text, case-insensitive, empty = off', () => {
    const row = { n: 1, ctx: 0, delta: 0, out: 0, reason: 0, in: 0, cr: 0, text: '10/5/2026, 3:24:15 PM' };
    assert.equal(L.callRowMatches(row, cf()), true);
    assert.equal(L.callRowMatches(row, cf({ text: '3:24' })), true);
    assert.equal(L.callRowMatches(row, cf({ text: 'PM' })), true);
    assert.equal(L.callRowMatches(row, cf({ text: '2025' })), false);
});

test('visibleCallRows: delta computed against the full list, then filtered', () => {
    const calls = [call({ ctx: 100 }), call({ ctx: 150 }), call({ ctx: 120 }), call({ ctx: 400 })];
    const vis = L.visibleCallRows(calls, cf({ deltaMin: 60 }));
    assert.deepEqual(vis.map(r => r.n), [1, 4]);
    assert.equal(vis[1].delta, 280);
    const vis2 = L.visibleCallRows(calls, cf());
    assert.equal(vis2.length, 4);
    assert.deepEqual(vis2.map(r => r.delta), [100, 50, -30, 280]);
});

test('visibleCallRows: all active criteria AND together', () => {
    const calls = [call({ ctx: 100, out: 10 }), call({ ctx: 900, out: 90 })];
    const vis = L.visibleCallRows(calls, cf({ ctxMin: 500, outMin: 50 }));
    assert.deepEqual(vis.map(r => r.n), [2]);
});
