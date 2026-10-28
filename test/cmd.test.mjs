import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// The command layout and search detection live inside index.html between
// CMD-LOGIC marker comments; this runs that exact block in a sandbox.
const html = readFileSync(new URL('../src/index.html', import.meta.url), 'utf8');
const block = /\/\/\s*CMD-LOGIC-START([\s\S]*?)\/\/\s*CMD-LOGIC-END/.exec(html);
assert.ok(block, 'CMD-LOGIC block not found in index.html');
const mod = { exports: {} };
vm.runInNewContext(block[1] + '\nmodule.exports = {cmdParts, searchOnly};', { module: mod });
const { cmdParts, searchOnly } = mod.exports;
const lines = s => cmdParts(s).map(p => p.text).join('');

test('cmdParts: one command per line, pipes end the line, quotes kept', () => {
    assert.equal(
        lines('cd /x && grep -rln "a|b" src --include=*.go | grep -v _test; grep -n "s\\|c" f.go | sed -n 1,20p'),
        'cd /x &&\ngrep -rln "a|b" src --include=*.go |\ngrep -v _test;\ngrep -n "s\\|c" f.go |\nsed -n 1,20p');
});

test('searchOnly: chains of search commands', () => {
    assert.equal(searchOnly('Bash', 'cd /x && grep -rln "rows.Next()" src | grep -v _test; sed -n 1,20p f.go'), true);
    assert.equal(searchOnly('Bash', 'cd /x && ls deploy 2>/dev/null | head -5; cat a.go 2>&1 | wc -l'), true);
    assert.equal(searchOnly('Bash', 'find . -name "*.go" | grep -c x'), true);
    assert.equal(searchOnly('Bash', 'grep "a > b" f'), true);
    assert.equal(searchOnly('Bash', 'grep -rn x f | head; grep y g | head -5'), true);
    assert.equal(searchOnly('Bash', 'sed -n 1,9p a; echo -----; git log --oneline -3 && git -C /x diff HEAD'), true);
});

test('searchOnly: loops, tests and assignments around search commands', () => {
    assert.equal(searchOnly('Bash', `cd /x && awk 'NR>=2630' c.yaml | grep -nE "^[a-z]+:"; for s in n2 n3; do n=$(grep -n "^$s:" c.yaml | cut -d: -f1); echo "== $s at $n"; [ -n "$n" ] && sed -n "$n,$((n+80))p" c.yaml | grep -nE "db_|auth"; done`), true);
    assert.equal(searchOnly('Bash', 'for f in a b; do if [ -f "$f" ]; then head -3 "$f"; fi; done'), true);
    assert.equal(searchOnly('Bash', 'while read l; do echo "$l"; done < list.txt'), true);
});

test('searchOnly: loops and substitutions hiding writes are not a search', () => {
    assert.equal(searchOnly('Bash', 'for f in a b; do rm "$f"; done'), false);
    assert.equal(searchOnly('Bash', 'n=$(rm -rf x); echo $n'), false);
    assert.equal(searchOnly('Bash', 'echo "$(touch x)"'), false);
    assert.equal(searchOnly('Bash', 'for f in $(curl -X DELETE u); do echo $f; done'), false);
    assert.equal(searchOnly('Bash', 'for f in a; do echo $f; done > out.txt'), false);
    assert.equal(searchOnly('Bash', 'X=1 go test ./...'), false);
});

test('searchOnly: other programs or writes are not a search', () => {
    assert.equal(searchOnly('Bash', 'cd /x && go test ./...'), false);
    assert.equal(searchOnly('Bash', "sed -i 's/a/b/' f.go"), false);
    assert.equal(searchOnly('Bash', 'find . -name x -delete'), false);
    assert.equal(searchOnly('Bash', 'cat a > b'), false);
    assert.equal(searchOnly('Bash', 'git log -1 && git commit -m x'), false);
    assert.equal(searchOnly('Bash', 'grep x f >> out.txt'), false);
    assert.equal(searchOnly('Bash', "cat <<'E' | head\nx\nE"), false);
    assert.equal(searchOnly('Bash', ''), false);
    assert.equal(searchOnly('Edit', 'file_path: /a'), false);
});

test('searchOnly: read-only queries with curl, jq and shell functions', () => {
    assert.equal(searchOnly('Bash', `P=https://prom/api/v1/query; q(){ curl -s -m 30 "$P" --data-urlencode "query=$1"; echo; }
q 'count(up) by (dc)' | jq -c '.data.result[]'
now=$(date +%s); s=$((now-7200))
for dc in n2 n3; do q "up{dc=\"$dc\"}" | jq -r .status; done > /tmp/b.txt; wc -l < /tmp/b.txt
join -j1 <(awk '{print $1}' /tmp/a.txt) <(sort /tmp/b.txt) | sort`), true);
    assert.equal(searchOnly('Bash', 'git branch -a 2>&1 | grep -i x; git ls-tree -r --name-only main | head'), true);
});

test('searchOnly: writing curl, date or git calls are not a search', () => {
    assert.equal(searchOnly('Bash', 'curl -X POST -d x u'), false);
    assert.equal(searchOnly('Bash', 'curl -s u -o out.json'), false);
    assert.equal(searchOnly('Bash', 'curl -T f u'), false);
    assert.equal(searchOnly('Bash', 'date -s 10:00'), false);
    assert.equal(searchOnly('Bash', 'git branch new-feature'), false);
    assert.equal(searchOnly('Bash', 'q(){ rm -rf "$1"; }; q x'), false);
    assert.equal(searchOnly('Bash', 'diff <(rm x) y'), false);
});

test('searchOnly: built-in search tools', () => {
    assert.equal(searchOnly('Read', ''), true);
    assert.equal(searchOnly('Grep', 'pattern: x'), true);
    assert.equal(searchOnly('glob', ''), true);
});
