import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// The pure filter logic lives inside index.html between marker comments.
// This harness extracts that block and runs it in a sandbox, so the tests
// always exercise the exact code shipped to the browser.
const html = readFileSync(new URL('../src/index.html', import.meta.url), 'utf8');
const block = /\/\/\s*FILTER-LOGIC-START([\s\S]*?)\/\/\s*FILTER-LOGIC-END/.exec(html);
assert.ok(block, 'FILTER-LOGIC block not found in index.html');
const mod = { exports: {} };
vm.runInNewContext(block[1], { module: mod, console });
const L = mod.exports;

const node = (o = {}) => ({
    id: 'n', parent: null, title: '(untitled)', agent: '', model: null, dir: '',
    maxctx: 0, in: 0, out: 0, reason: 0, cr: 0, cw: 0, created: 0, updated: 0, dur: 0,
    active: false, children: [], subtree: { in: 0, out: 0, reason: 0, cr: 0, cw: 0 }, ...o,
});
const tf = (o = {}) => ({
    text: '', models: new Set(), providers: new Set(), dirs: new Set(),
    numMin: null, numMax: null,
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

test('tree spent filter: min/max in DOLLARS, inclusive (in=1M at $4/1M = $4)', () => {
    L.resetCost();
    const n = node({ subtree: { in: 1000000, out: 0, reason: 0, cr: 0, cw: 0 } });
    assert.equal(L.treeMatchesNode(n, tf({ spentMin: 4 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ spentMin: 4.01 })), false);
    assert.equal(L.treeMatchesNode(n, tf({ spentMax: 4 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ spentMax: 3.99 })), false);
});

test('costOf: sums enabled token fields at their $/1M prices', () => {
    L.resetCost();
    assert.equal(L.costOf({in: 1000000, out: 0, reason: 0, cr: 0, cw: 0}), 4);
    assert.equal(L.costOf({in: 0, out: 1000000, reason: 0, cr: 0, cw: 0}), 20);
    assert.equal(L.costOf({in: 0, out: 0, reason: 0, cr: 5000000, cw: 0}), 1);
    assert.equal(L.costOf({in: 1000000, out: 1000000, reason: 1000000, cr: 5000000, cw: 1000000}), 50);
    L.COST.in.on = false;
    assert.equal(L.costOf({in: 1000000, out: 0, reason: 0, cr: 0, cw: 0}), 0);
    assert.equal(L.costOf({in: 1000000, out: 1000000, reason: 0, cr: 0, cw: 0}), 20);
});

test('tokEqOf: money-weighted tokens normalized to fresh input', () => {
    L.resetCost();
    assert.equal(L.tokEqOf({in: 1000, out: 0, reason: 0, cr: 0, cw: 0}), 1000);
    assert.equal(L.tokEqOf({in: 1000, out: 1000, reason: 0, cr: 0, cw: 0}), 1000 + 1000 * 20 / 4);
    assert.equal(L.tokEqOf({in: 0, out: 0, reason: 1000, cr: 0, cw: 1000}), 1000 * 20 / 4 + 1000 * 5 / 4);
    assert.ok(Math.abs(L.tokEqOf({in: 0, out: 0, reason: 0, cr: 1000000, cw: 0}) - 1000000 * 0.2 / 4) < 1e-6);
    L.COST.out.on = false;
    assert.equal(L.tokEqOf({in: 1000, out: 1000, reason: 0, cr: 0, cw: 0}), 1000);
    L.resetCost();
    L.COST.in.price = 0;
    assert.equal(L.tokEqOf({in: 1000, out: 2000, reason: 0, cr: 0, cw: 0}), 1000 + 2000 * 20);
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
    L.resetCost();
    const n = node({
        title: 'fix bug', model: { providerID: 'openai', modelID: 'gpt-5' },
        maxctx: 10000, subtree: { in: 100, out: 100, reason: 100, cr: 0, cw: 0 },
    });
    const f = tf({ text: 'bug', models: new Set(['gpt-5']), ctxMin: 5, spentMax: 0.005 });
    assert.equal(L.treeMatchesNode(n, f), true);
    assert.equal(L.treeMatchesNode(n, tf({ ...f, models: new Set(['other']) })), false);
    assert.equal(L.treeMatchesNode(n, tf({ ...f, ctxMin: 20 })), false);
    assert.equal(L.treeMatchesNode(n, tf({ ...f, text: 'other' })), false);
});

test('tree order filter: min/max on node order number, inclusive', () => {
    const n = node({ order: 3 });
    assert.equal(L.treeMatchesNode(n, tf({ numMin: 3 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ numMax: 3 })), true);
    assert.equal(L.treeMatchesNode(n, tf({ numMin: 4 })), false);
    assert.equal(L.treeMatchesNode(n, tf({ numMax: 2 })), false);
});

test('tree order filter: node without order counts as 0', () => {
    assert.equal(L.treeMatchesNode(node(), tf({ numMin: 1 })), false);
    assert.equal(L.treeMatchesNode(node(), tf({ numMax: 0 })), true);
});

test('tree node order criterion ANDs together with the rest', () => {
    const n = node({ order: 2, title: 'fix bug' });
    const f = tf({ text: 'bug', numMin: 2 });
    assert.equal(L.treeMatchesNode(n, f), true);
    assert.equal(L.treeMatchesNode(n, tf({ ...f, numMin: 3 })), false);
});

test('tree folder filter: empty set = off, empty dir = (unknown), AND with model filter', () => {
    const n = node({ dir: '/home/me/project' });
    const u = node({ dir: '' });
    assert.equal(L.nodeDir(n), '/home/me/project');
    assert.equal(L.nodeDir(u), '(unknown)');
    assert.equal(L.treeMatchesNode(n, tf()), true);
    assert.equal(L.treeMatchesNode(n, tf({ dirs: new Set(['/home/me/project']) })), true);
    assert.equal(L.treeMatchesNode(n, tf({ dirs: new Set(['/other']) })), false);
    assert.equal(L.treeMatchesNode(u, tf({ dirs: new Set(['(unknown)']) })), true);
    assert.equal(L.treeMatchesNode(n, tf({ dirs: new Set(['/home/me/project']), models: new Set(['zzz']) })), false);
});

test('treeCheckedVisible: node or any descendant in the marked set', () => {
    const root = node({ id: 'r', children: [node({ id: 'a' }), node({ id: 'b', children: [node({ id: 'c' })] })] });
    assert.equal(L.treeCheckedVisible(root, new Set()), false);
    assert.equal(L.treeCheckedVisible(root, new Set(['r'])), true);
    assert.equal(L.treeCheckedVisible(root, new Set(['c'])), true);
    assert.equal(L.treeCheckedVisible(root, new Set(['a', 'x'])), true);
    assert.equal(L.treeCheckedVisible(node({ id: 's' }), new Set(['s'])), true);
    assert.equal(L.treeCheckedVisible(node({}), new Set(['s'])), false);
});

test('treeFilterCount: dirs count as a criterion group', () => {
    assert.equal(L.treeFilterCount(tf()), 0);
    assert.equal(L.treeFilterCount(tf({ dirs: new Set(['/a']) })), 1);
    assert.equal(L.treeFilterCount(tf({ text: 'x', dirs: new Set(['/a']), ctxMin: 1 })), 3);
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

test('hotLevel: strictly above kilo thresholds, null = off, crit wins over warn', () => {
    assert.equal(L.hotLevel(120001, 100, 120), 'crit');
    assert.equal(L.hotLevel(120000, 100, 120), 'warn');
    assert.equal(L.hotLevel(100001, 100, 120), 'warn');
    assert.equal(L.hotLevel(100000, 100, 120), '');
    assert.equal(L.hotLevel(999999, null, null), '');
    assert.equal(L.hotLevel(150000, null, 120), 'crit');
    assert.equal(L.hotLevel(150000, 100, null), 'warn');
    assert.equal(L.hotLevel(110000, null, 120), '');
    assert.equal(L.hotLevel(500, 0.3, 0.4), 'crit');
    assert.equal(L.hotLevel(350, 0.3, 0.4), 'warn');
    assert.equal(L.hotLevel(300, 0.3, 0.4), '');
});

test('treeHotVisible: node or any descendant above warn or crit', () => {
    const root = node({ maxctx: 50000, children: [node({ maxctx: 150000 })] });
    assert.equal(L.treeHotVisible(root, 100, 120), true);
    assert.equal(L.treeHotVisible(node({ maxctx: 50000 }), 100, 120), false);
    assert.equal(L.treeHotVisible(node({ maxctx: 110000 }), 100, 120), true);
    assert.equal(L.treeHotVisible(node({ maxctx: 150000 }), 100, 120), true);
    assert.equal(L.treeHotVisible(node({ maxctx: 999999 }), null, null), false);
});

test('treeRecentVisible: active, or node or any descendant updated since the window start', () => {
    const since = 1000;
    assert.equal(L.treeRecentVisible(node({ updated: 999 }), since), false);
    assert.equal(L.treeRecentVisible(node({ updated: 1000 }), since), true);
    assert.equal(L.treeRecentVisible(node({ updated: 10, active: true }), since), true);
    assert.equal(L.treeRecentVisible(node({ updated: 10, children: [node({ updated: 2000 })] }), since), true);
});

test('recent-window slider: log scale 5 min..24 h, round trip, coarser rounding', () => {
    assert.equal(L.recWinAt(0), 5);
    assert.equal(L.recWinAt(1000), 24 * 60);
    for (const m of [5, 15, 30, 45, 60, 180, 24 * 60]) assert.equal(L.recWinAt(L.recSliderPos(m)), m);
    assert.equal(L.recWinAt(800) % 15, 0);
});

test('treeActiveVisible: node or any descendant active', () => {
    const root = node({ children: [node({ active: true })] });
    assert.equal(L.treeActiveVisible(root), true);
    assert.equal(L.treeActiveVisible(node({})), false);
    assert.equal(L.treeActiveVisible(node({ active: true })), true);
    assert.equal(L.treeActiveVisible(node({ children: [node({})] })), false);
});

test('heartSizes: ranks active sessions by updated desc — top xl, second lg, rest regular', () => {
    const roots = [
        node({ id: 'a', active: true, updated: 3000 }),
        node({ id: 'b', active: true, updated: 2000 }),
        node({ id: 'c', active: true, updated: 1000 }),
        node({ id: 'd', updated: 9000 }),
    ];
    const m = L.heartSizes(roots);
    assert.equal(m.get('a'), 'xl');
    assert.equal(m.get('b'), 'lg');
    assert.equal(m.get('c'), '');
    assert.equal(m.has('d'), false);
});

test('heartSizes: ranks across nested children, not per-branch', () => {
    const roots = [
        node({ id: 'p', updated: 100, children: [node({ id: 'k', active: true, updated: 5000 })] }),
        node({ id: 'q', active: true, updated: 4000 }),
    ];
    const m = L.heartSizes(roots);
    assert.equal(m.get('k'), 'xl');
    assert.equal(m.get('q'), 'lg');
    assert.equal(m.has('p'), false);
});

test('heartSizes: single active session gets xl and nothing gets lg; none active = empty map', () => {
    const m = L.heartSizes([node({ id: 'x', active: true, updated: 900 })]);
    assert.equal(m.get('x'), 'xl');
    const m2 = L.heartSizes([node({ id: 'y', updated: 900 })]);
    assert.equal(m2.size, 0);
});

test('heartSizes: tie on updated keeps insertion order for the top ranks', () => {
    const roots = [
        node({ id: 'f', active: true, updated: 700 }),
        node({ id: 's', active: true, updated: 700 }),
    ];
    const m = L.heartSizes(roots);
    assert.equal(m.get('f'), 'xl');
    assert.equal(m.get('s'), 'lg');
});

test('ecgPoints: no beats = empty trace (no flatline from the past)', () => {
    const p = L.ecgPoints([], 1000000, { win: 1000, w: 40, h: 20 });
    assert.equal(p, '');
});

test('ecgPoints: a call is a triangle from sent to finished, peak halfway between', () => {
    const now = 1000000;
    const p = L.ecgPoints([[now - 500, now - 250]], now, { win: 1000, w: 40, h: 20 });
    const pts = p.split(' ').map(s => s.split(',').map(Number));
    // base = 17, top = 3; sent at x=20, finished at x=30, then flat to the edge
    assert.deepEqual(pts, [[20, 17], [25, 3], [30, 17], [40, 17]]);
});

test('ecgPoints: an in-flight call (finished = 0) climbs to a peak at now', () => {
    const now = 1000000;
    const pts = L.ecgPoints([[now - 500, 0]], now, { win: 1000, w: 40, h: 20 })
        .split(' ').map(s => s.split(',').map(Number));
    assert.deepEqual(pts, [[20, 17], [40, 3]]);
});

test('ecgPoints: idle gap between calls stays at zero', () => {
    const now = 1000000;
    const pts = L.ecgPoints([[now - 900, now - 800], [now - 300, now - 200]], now, { win: 1000, w: 40, h: 20 })
        .split(' ').map(s => s.split(',').map(Number));
    assert.deepEqual(pts.slice(2, 4), [[8, 17], [28, 17]], 'flat at zero from the first finish to the next send');
});

test('ecgRises: one bold rising edge per request, up to its peak', () => {
    const now = 1000000;
    const d = L.ecgRises([[now - 900, now - 800], [now - 300, 0]], now, { win: 1000, w: 40, h: 20 });
    assert.equal(d, 'M4.0,17.0L6.0,3.0M28.0,17.0L40.0,3.0');
});

test('ecgPoints: beats outside the window are ignored', () => {
    const now = 1000000;
    const p = L.ecgPoints([[now - 5000, now - 4000], [now + 500, now + 600]], now, { win: 1000, w: 40, h: 20 });
    assert.equal(p, '');
});

test('ecgPoints: unsorted and overlapping beats still produce monotonically increasing x', () => {
    const now = 1000000;
    const p = L.ecgPoints([[now - 200, now - 100], [now - 800, now - 150], [now - 900, now - 850]], now, { win: 1000, w: 40, h: 20 });
    const xs = p.split(' ').map(s => Number(s.split(',')[0]));
    assert.deepEqual([...xs].sort((a, b) => a - b).map(Number), xs);
});

test('ecgPoints: x position depends only on (beat - now), so all rows share one timeline', () => {
    const p1 = L.ecgPoints([[1500, 1600]], 2000, { win: 1000, w: 40, h: 20 });
    const p2 = L.ecgPoints([[11500, 11600]], 12000, { win: 1000, w: 40, h: 20 });
    assert.equal(p1, p2);
});

test('ecgPoints: call sent right now stays clamped inside the box and visible', () => {
    const now = 1000000;
    const p = L.ecgPoints([[now, 0]], now, { win: 1000, w: 40, h: 20 });
    const pts = p.split(' ').map(s => s.split(',').map(Number));
    assert.ok(pts.every(([x]) => x <= 40), 'no x beyond width');
    assert.ok(pts.some(([x, y]) => y === 3 && x === 40), 'peak drawn at now');
    assert.ok(pts[0][0] < 40, 'rise starts inside the box');
});

test('callDeltaAt: first call delta = its ctx, later = growth vs previous', () => {
    const calls = [call({ ctx: 100 }), call({ ctx: 150 }), call({ ctx: 120 })];
    assert.equal(L.callDeltaAt(calls, 0), 100);
    assert.equal(L.callDeltaAt(calls, 1), 50);
    assert.equal(L.callDeltaAt(calls, 2), -30);
});

test('callRowMatches: min/max on every column', () => {
    const row = { n: 3, ctx: 1000000, delta: 200000, out: 50000, reason: 30000, in: 120000, cr: 800000, text: '10/5/2026, 3:24:15 PM' };
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
    const calls = [call({ ctx: 100000 }), call({ ctx: 150000 }), call({ ctx: 120000 }), call({ ctx: 400000 })];
    const vis = L.visibleCallRows(calls, cf({ deltaMin: 60 }));
    assert.deepEqual(vis.map(r => r.n), [1, 4]);
    assert.equal(vis[1].delta, 280000);
    const vis2 = L.visibleCallRows(calls, cf());
    assert.equal(vis2.length, 4);
    assert.deepEqual(vis2.map(r => r.delta), [100000, 50000, -30000, 280000]);
});

test('visibleCallRows: all active criteria AND together', () => {
    const calls = [call({ ctx: 100000, out: 10000 }), call({ ctx: 900000, out: 90000 })];
    const vis = L.visibleCallRows(calls, cf({ ctxMin: 500, outMin: 50 }));
    assert.deepEqual(vis.map(r => r.n), [2]);
});

test('visibleCallRows: propagates hu (human-trigger kind) as string', () => {
    const calls = [call({hu: 'text', ctx: 100}), call({ctx: 150}), call({hu: 'image', ctx: 120})];
    const vis = L.visibleCallRows(calls, cf());
    assert.deepEqual(vis.map(r => r.hu), ['text', '', 'image']);
});

test('displayTitle: drops the "(@agent subagent)" suffix only for the same agent', () => {
    assert.equal(L.displayTitle('Fix bug (@general subagent)', 'general'), 'Fix bug');
    assert.equal(L.displayTitle('Fix bug (@general subagent)', 'explore'), 'Fix bug (@general subagent)');
    assert.equal(L.displayTitle('Fix bug (@general subagent)', ''), 'Fix bug (@general subagent)');
    assert.equal(L.displayTitle('(@general subagent)', 'general'), '(@general subagent)');
    assert.equal(L.displayTitle('Plain title', 'general'), 'Plain title');
    assert.equal(L.displayTitle(null, 'general'), '');
});

test('treeFilterCount: one per active criterion group', () => {
    assert.equal(L.treeFilterCount(tf()), 0);
    assert.equal(L.treeFilterCount(tf({text: 'x', models: new Set(['a', 'b']), ctxMin: 1, ctxMax: 5})), 3);
    assert.equal(L.treeFilterCount(tf({durMax: 0})), 1);
});

test('callFilterCount: one per active criterion group', () => {
    assert.equal(L.callFilterCount(cf()), 0);
    assert.equal(L.callFilterCount(cf({text: 'a', deltaMin: -5, crMax: 0})), 3);
});

test('parseHashSel: reads the session id from the url hash', () => {
    assert.equal(L.parseHashSel(''), null);
    assert.equal(L.parseHashSel('#session='), null);
    assert.equal(L.parseHashSel('#session=ses_1'), 'ses_1');
    assert.equal(L.parseHashSel('#x=1&session=a%2Fb'), 'a/b');
    assert.equal(L.parseHashSel('#session=%E0%A4%A'), null);
});

test('exportCandidates: favourites plus top-level sessions updated within the last hour, newest first', () => {
    const now = 10 * 3600e3;
    const tree = [
        node({ id: 'old', updated: now - 3 * 3600e3 }),
        node({ id: 'fav', updated: now - 5 * 3600e3 }),
        node({ id: 'recent', updated: now - 600e3 }),
        node({ id: 'edge', updated: now - 3600e3 }),
        node({ id: 'newest', updated: now - 1000 }),
    ];
    const ids = L.exportCandidates(tree, new Set(['fav']), now).map(n => n.id);
    assert.deepEqual(ids, ['newest', 'recent', 'edge', 'fav']);
});

test('exportCandidates: only top-level nodes are considered, never children', () => {
    const now = 1e7;
    const tree = [node({ id: 'root', updated: 0, children: [node({ id: 'kid', updated: now })] })];
    assert.deepEqual(L.exportCandidates(tree, new Set(), now), []);
});

test('treeSpanVisible: node or any descendant active at some moment of [from, to)', () => {
    const [from, to] = [1000, 2000];
    assert.equal(L.treeSpanVisible(node({ created: 500, updated: 999 }), from, to), false);
    assert.equal(L.treeSpanVisible(node({ created: 500, updated: 1000 }), from, to), true);
    assert.equal(L.treeSpanVisible(node({ created: 500, updated: 3000 }), from, to), true);
    assert.equal(L.treeSpanVisible(node({ created: 1999, updated: 0 }), from, to), true);
    assert.equal(L.treeSpanVisible(node({ created: 2000, updated: 2500 }), from, to), false);
    const parent = node({ created: 100, updated: 200, children: [node({ created: 1500, updated: 1600 })] });
    assert.equal(L.treeSpanVisible(parent, from, to), true);
});

test('periodRange: local day and Monday-based week, shifted by whole periods', () => {
    const wed = new Date(2026, 9, 21, 15, 30).getTime(); // Wed 21 Oct 2026
    assert.deepEqual([...L.periodRange('day', wed, 0)], [new Date(2026, 9, 21).getTime(), new Date(2026, 9, 22).getTime()]);
    assert.deepEqual([...L.periodRange('day', wed, -1)], [new Date(2026, 9, 20).getTime(), new Date(2026, 9, 21).getTime()]);
    assert.deepEqual([...L.periodRange('week', wed, 0)], [new Date(2026, 9, 19).getTime(), new Date(2026, 9, 26).getTime()]);
    assert.deepEqual([...L.periodRange('week', wed, -1)], [new Date(2026, 9, 12).getTime(), new Date(2026, 9, 19).getTime()]);
    const sun = new Date(2026, 9, 25, 10).getTime();
    assert.deepEqual(L.periodRange('week', sun, 0)[0], new Date(2026, 9, 19).getTime());
});

test('shiftSpan: whole-day periods move by calendar days, others by their length', () => {
    const a = new Date(2026, 9, 19).getTime(), b = new Date(2026, 9, 24).getTime();
    assert.deepEqual([...L.shiftSpan(a, b, -1)], [new Date(2026, 9, 14).getTime(), a]);
    assert.deepEqual([...L.shiftSpan(a, b, 1)], [b, new Date(2026, 9, 29).getTime()]);
    const h = 3600000, c = new Date(2026, 9, 19, 10).getTime();
    assert.deepEqual([...L.shiftSpan(c, c + 3 * h, 1)], [c + 3 * h, c + 6 * h]);
});

test('spanActivity: counts every node in each bucket its [created, updated] touches', () => {
    const tree = [
        node({ created: 10, updated: 35, children: [node({ created: 20, updated: 25 })] }),
        node({ created: 5, updated: 8 }),   // ends before the window
        node({ created: 0, updated: 50 }),  // no created time — skipped
        node({ created: 45, updated: 0 }),  // never updated — counts at created
    ];
    assert.deepEqual([...L.spanActivity(tree, 10, 50, 10)], [1, 2, 1, 1]);
});
