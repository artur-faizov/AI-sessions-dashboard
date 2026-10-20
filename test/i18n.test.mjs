import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// The UI text resources live in src/i18n.js. This harness runs the exact file
// shipped to the browser inside a sandbox, with a localStorage stub.
const src = readFileSync(new URL('../src/i18n.js', import.meta.url), 'utf8');

function makeLS(initial = {}) {
    const m = new Map(Object.entries(initial));
    return {
        getItem: k => (m.has(k) ? m.get(k) : null),
        setItem: (k, v) => m.set(k, String(v)),
        removeItem: k => m.delete(k),
    };
}

function load(ls) {
    const mod = { exports: {} };
    vm.runInNewContext(src, { module: mod, console, localStorage: ls });
    return mod.exports;
}

test('defaults to english when nothing is stored', () => {
    const I = load(makeLS());
    assert.equal(I.curLang(), 'en');
    assert.equal(I.tr('app.title'), 'Token Tree');
});

test('setLang switches the dictionary', () => {
    const I = load(makeLS());
    I.setLang('ru');
    assert.equal(I.curLang(), 'ru');
    assert.equal(I.tr('app.title'), 'Дерево токенов');
});

test('interpolates {n}-style params', () => {
    const I = load(makeLS());
    assert.equal(I.tr('refresh.title', { n: 5 }), 'auto-refresh in 5s · click to refresh now');
});

test('english plural arrays pick one/other by n', () => {
    const I = load(makeLS());
    assert.equal(I.tr('tree.sessions', { n: 1 }), '1 session');
    assert.equal(I.tr('tree.sessions', { n: 3 }), '3 sessions');
});

test('russian plural arrays pick one/few/many by n', () => {
    const I = load(makeLS());
    I.setLang('ru');
    assert.equal(I.tr('tree.sessions', { n: 1 }), '1 сессия');
    assert.equal(I.tr('tree.sessions', { n: 2 }), '2 сессии');
    assert.equal(I.tr('tree.sessions', { n: 5 }), '5 сессий');
    assert.equal(I.tr('tree.sessions', { n: 11 }), '11 сессий');
    assert.equal(I.tr('tree.sessions', { n: 21 }), '21 сессия');
});

test('a key missing in the current language falls back to english', () => {
    const I = load(makeLS());
    I.setLang('ru');
    const key = 'app.title';
    const saved = I.dicts.ru[key];
    delete I.dicts.ru[key]; // simulate an untranslated key
    try {
        assert.equal(I.tr(key), I.dicts.en[key]);
    } finally {
        I.dicts.ru[key] = saved;
    }
});

test('an unknown key returns the key itself', () => {
    const I = load(makeLS());
    assert.equal(I.tr('no.such.key'), 'no.such.key');
});

test('every en key exists in ru and vice versa', () => {
    const I = load(makeLS());
    const en = Object.keys(I.dicts.en), ru = Object.keys(I.dicts.ru);
    assert.deepEqual(ru.filter(k => !(k in I.dicts.en)), []);
    assert.deepEqual(en.filter(k => !(k in I.dicts.ru)), []);
});

test('every value is a string or an array of strings', () => {
    const I = load(makeLS());
    for (const dict of Object.values(I.dicts)) {
        for (const [k, v] of Object.entries(dict)) {
            const bad = Array.isArray(v) ? v.some(x => typeof x !== 'string') : typeof v !== 'string';
            assert.ok(!bad, `${k} must be a string or array of strings`);
        }
    }
});

test('setLang persists the choice and it is restored on load', () => {
    const ls = makeLS();
    const A = load(ls);
    A.setLang('ru');
    assert.equal(ls.getItem('lang'), 'ru');
    const B = load(ls); // fresh page load, same localStorage
    assert.equal(B.curLang(), 'ru');
});

test('setLang with an unknown code keeps the current language', () => {
    const I = load(makeLS());
    I.setLang('ru');
    I.setLang('de');
    assert.equal(I.curLang(), 'ru');
});

test('exposes the language list with display labels and locales', () => {
    const I = load(makeLS());
    assert.deepEqual([...I.LANGS.map(l => l.code)], ['en', 'ru']);
    assert.equal(I.locale(), 'en-US');
    I.setLang('ru');
    assert.equal(I.locale(), 'ru-RU');
});

test('every key used by index.html exists in both dictionaries', () => {
    const I = load(makeLS());
    const html = readFileSync(new URL('../src/index.html', import.meta.url), 'utf8');
    const used = new Set();
    for (const m of html.matchAll(/\btr\('([^']+)'/g)) used.add(m[1]);
    assert.ok(used.size > 50, `expected many tr() calls, found ${used.size}`);
    const missing = [...used].filter(k => !(k in I.dicts.en) || !(k in I.dicts.ru));
    assert.deepEqual(missing, []);
});
