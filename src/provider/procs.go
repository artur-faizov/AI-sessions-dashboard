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

// ProcChildren is ChildStarts; tests replace it to stay independent of the host's processes.
var ProcChildren = ChildStarts

// clkTck is USER_HZ, the unit of /proc/<pid>/stat starttime (100 on Linux).
const clkTck = 100

// ChildStarts lists, per working directory of the processes named name, the
// start times (unix ms) of their direct children: a tool's command shows up
// as a child that started after the tool did, while long-lived children (LSP,
// MCP servers) started earlier. A directory with such a process but no
// children maps to an empty slice. It returns nil when /proc is unavailable.
func ChildStarts(name string) map[string][]int64 {
	boot := bootMS()
	ents, err := os.ReadDir("/proc")
	if err != nil || boot == 0 {
		return nil
	}
	owners := map[string]string{} // pid of a process named name -> its cwd
	var others []string
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		base := filepath.Join("/proc", e.Name())
		if procNamed(base, name) {
			if cwd, err := os.Readlink(filepath.Join(base, "cwd")); err == nil {
				owners[e.Name()] = cwd
			}
			continue
		}
		others = append(others, e.Name())
	}
	out := map[string][]int64{}
	for _, cwd := range owners {
		out[cwd] = []int64{}
	}
	for _, pid := range others {
		ppid, start := procStat(filepath.Join("/proc", pid))
		if cwd, ok := owners[ppid]; ok && start >= 0 {
			out[cwd] = append(out[cwd], boot+start*1000/clkTck)
		}
	}
	return out
}

// bootMS is the boot time (unix ms) from /proc/stat, 0 when unreadable.
func bootMS() int64 {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(ln, "btime "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n * 1000
		}
	}
	return 0
}

// procStat reads the parent pid and the start time (clock ticks since boot)
// from /proc/<pid>/stat; ppid is "" and start -1 when unreadable. The comm
// field may hold spaces and parens, so fields are counted after the last ')'.
func procStat(base string) (ppid string, start int64) {
	b, err := os.ReadFile(filepath.Join(base, "stat"))
	if err != nil {
		return "", -1
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", -1
	}
	f := strings.Fields(s[i+1:]) // f[0] is field 3 (state)
	if len(f) < 20 {
		return "", -1
	}
	start, err = strconv.ParseInt(f[19], 10, 64)
	if err != nil {
		return "", -1
	}
	return f[1], start
}
