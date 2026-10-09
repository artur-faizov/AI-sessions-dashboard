package provider

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ProcDirs is OpenDirs; tests replace it to stay independent of the host's processes.
var ProcDirs = OpenDirs

// OpenDirs counts running processes per working directory, for processes named name
// (matched on /proc comm or argv[0] basename). It returns nil whenever the
// answer is unreliable — no /proc, unreadable entries only, or no matching
// process at all — so callers fall back to the recency heuristic.
func OpenDirs(name string) map[string]int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	dirs := map[string]int{}
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		base := filepath.Join("/proc", e.Name())
		if !procNamed(base, name) {
			continue
		}
		if cwd, err := os.Readlink(filepath.Join(base, "cwd")); err == nil {
			dirs[cwd]++
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	return dirs
}

func procNamed(base, name string) bool {
	if b, err := os.ReadFile(filepath.Join(base, "comm")); err == nil && strings.TrimSpace(string(b)) == name {
		return true
	}
	if b, err := os.ReadFile(filepath.Join(base, "cmdline")); err == nil {
		argv0, _, _ := strings.Cut(string(b), "\x00")
		return filepath.Base(argv0) == name
	}
	return false
}

// DropClosed deactivates root sessions (and their subtrees) that no running
// process accounts for: in each directory only as many of the most recently
// updated active roots stay live as there are processes there. A nil dirs
// (detection unavailable) leaves everything untouched.
func DropClosed(roots []*SessionNode, dirs map[string]int) {
	if dirs == nil {
		return
	}
	byDir := map[string][]*SessionNode{}
	for _, r := range roots {
		if r.Active {
			byDir[r.Dir] = append(byDir[r.Dir], r)
		}
	}
	var off func(n *SessionNode)
	off = func(n *SessionNode) {
		n.Active = false
		for _, c := range n.Children {
			off(c)
		}
	}
	for dir, rs := range byDir {
		sort.Slice(rs, func(i, j int) bool { return rs[i].Updated > rs[j].Updated })
		for i := dirs[dir]; i < len(rs); i++ {
			off(rs[i])
		}
	}
}
