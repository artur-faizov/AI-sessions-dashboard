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
    text: '', models: new Set(), providers: new Set(),
    numMin: null, numMax: null,
    datePreset: null, dateFrom: null, dateTo: null, dateMin: null, dateMax: null,
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

test('tree date filter: created within inclusive ms range, created=0 passes open range', () => {
    const n = node({ created: new Date('2026-10-05T12:00').getTime() });
    assert.equal(L.treeMatchesNode(n, tf({
        dateMin: new Date('2026-10-05T00:00').getTime(),
        dateMax: new Date('2026-10-05T23:59:59.999').getTime(),
    })), true);
    assert.equal(L.treeMatchesNode(n, tf({ dateMin: new Date('2026-10-06T00:00').getTime() })), false);
    assert.equal(L.treeMatchesNode(n, tf({ dateMax: new Date('2026-10-04T23:59:59.999').getTime() })), false);
    assert.equal(L.treeMatchesNode(node({ created: 0 }), tf()), true);
});

test('presetRange: today = local midnight, no upper bound', () => {
    const now = new Date('2026-10-05T15:30').getTime();
    const [min, max] = L.presetRange('today', now);
    assert.equal(min, new Date('2026-10-05T00:00').getTime());
    assert.equal(max, null);
});

test('presetRange: week starts Monday — mid-week and Sunday both map to it', () => {
    const wed = new Date('2026-10-07T10:00').getTime();
    assert.equal(L.presetRange('week', wed)[0], new Date('2026-10-05T00:00').getTime());
    const sun = new Date('2026-10-11T23:00').getTime();
    assert.equal(L.presetRange('week', sun)[0], new Date('2026-10-05T00:00').getTime());
    const mon = new Date('2026-10-05T00:00').getTime();
    assert.equal(L.presetRange('week', mon)[0], mon);
});

test('presetRange: month = local midnight of the 1st, no upper bound', () => {
    const [min, max] = L.presetRange('month', new Date('2026-10-15T08:00').getTime());
    assert.equal(min, new Date('2026-10-01T00:00').getTime());
    assert.equal(max, null);
});

test('presetRange: null or unknown/custom preset = fully open range', () => {
    assert.deepEqual([...L.presetRange(null, 123)], [null, null]);
    assert.deepEqual([...L.presetRange(undefined, 123)], [null, null]);
    assert.deepEqual([...L.presetRange('custom', 123)], [null, null]);
});

test('dateStrToRange: YYYY-MM-DD pair -> inclusive local day bounds', () => {
    const [min, max] = L.dateStrToRange('2026-10-05', '2026-10-06');
    assert.equal(min, new Date('2026-10-05T00:00').getTime());
    assert.equal(max, new Date('2026-10-06T23:59:59.999').getTime());
});

test('dateStrToRange: empty or invalid side stays open', () => {
    assert.deepEqual([...L.dateStrToRange('', '2026-10-06')], [null, new Date('2026-10-06T23:59:59.999').getTime()]);
    assert.deepEqual([...L.dateStrToRange('2026-10-05', '')], [new Date('2026-10-05T00:00').getTime(), null]);
    assert.deepEqual([...L.dateStrToRange(null, null)], [null, null]);
    assert.deepEqual([...L.dateStrToRange('garbage', '')], [null, null]);
});

test('resolveDateBounds: preset resolved against the passed now, custom via strings', () => {
    const now = new Date('2026-10-07T15:00').getTime();
    assert.deepEqual([...L.resolveDateBounds(tf({ datePreset: 'today' }), now)], [...L.presetRange('today', now)]);
    assert.deepEqual(
        [...L.resolveDateBounds(tf({ datePreset: 'custom', dateFrom: '2026-10-05', dateTo: '2026-10-06' }), 0)],
        [...L.dateStrToRange('2026-10-05', '2026-10-06')],
    );
    assert.deepEqual([...L.resolveDateBounds(tf(), 123)], [null, null]);
});

test('tree node order and date criteria AND together with the rest', () => {
    const n = node({ order: 2, created: new Date('2026-10-05T12:00').getTime(), title: 'fix bug' });
    const f = tf({ text: 'bug', numMin: 2, dateMin: new Date('2026-10-05T00:00').getTime() });
    assert.equal(L.treeMatchesNode(n, f), true);
    assert.equal(L.treeMatchesNode(n, tf({ ...f, numMin: 3 })), false);
    assert.equal(L.treeMatchesNode(n, tf({ ...f, dateMin: new Date('2026-10-06T00:00').getTime() })), false);
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

test('ecgPoints: no beats = flat baseline from left to right edge', () => {
    const p = L.ecgPoints([], 1000000, { win: 1000, w: 40, h: 20 });
    assert.equal(p, '0.0,10.0 40.0,10.0');
});

test('ecgPoints: beat maps to an ECG spike — R peak above baseline at its position', () => {
    const now = 1000000;
    const p = L.ecgPoints([now - 500], now, { win: 1000, w: 40, h: 20 });
    const pts = p.split(' ').map(s => s.split(',').map(Number));
    assert.ok(pts.length > 2, 'spike has intermediate points');
    // R peak: x at beat position (half window = 20), y = mid - amp = 10 - 9 = 1
    assert.ok(pts.some(([x, y]) => Math.abs(x - 20) < 1 && y === 1), 'R peak at beat position above baseline');
    // dips below baseline exist (Q/S)
    assert.ok(pts.some(([x, y]) => y > 10), 'dip below baseline');
    assert.ok(pts.every(([x, y]) => x >= 0 && x <= 40 && y >= 0 && y <= 20), 'all points in bounds');
});

test('ecgPoints: beats outside the window are ignored', () => {
    const now = 1000000;
    const p = L.ecgPoints([now - 5000, now + 500], now, { win: 1000, w: 40, h: 20 });
    assert.equal(p, '0.0,10.0 40.0,10.0');
});

test('ecgPoints: unsorted beats still produce monotonically increasing x', () => {
    const now = 1000000;
    const p = L.ecgPoints([now - 200, now - 800], now, { win: 1000, w: 40, h: 20 });
    const xs = p.split(' ').map(s => Number(s.split(',')[0]));
    assert.deepEqual([...xs].sort((a, b) => a - b).map(Number), xs);
});

test('ecgPoints: beat at the right edge stays clamped inside the box', () => {
    const now = 1000000;
    const p = L.ecgPoints([now], now, { win: 1000, w: 40, h: 20 });
    const pts = p.split(' ').map(s => s.split(',').map(Number));
    assert.ok(pts.every(([x]) => x <= 40), 'no x beyond width');
    assert.ok(pts.some(([, y]) => y === 1), 'peak still drawn');
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
    assert.equal(L.treeFilterCount(tf({datePreset: 'today'})), 1);
    assert.equal(L.treeFilterCount(tf({datePreset: 'custom'})), 0);
    assert.equal(L.treeFilterCount(tf({datePreset: 'custom', dateTo: '2026-01-01', durMax: 0})), 2);
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
