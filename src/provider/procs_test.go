package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDirsFindsOwnProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	cwd, _ := os.Getwd()
	dirs := OpenDirs(filepath.Base(exe))
	if dirs == nil {
		t.Skip("no /proc")
	}
	if dirs[cwd] == 0 {
		t.Fatalf("own cwd %q missing from %v", cwd, dirs)
	}
}

func TestOpenDirsUnknownNameIsNil(t *testing.T) {
	if d := OpenDirs("no-such-process-xyz"); d != nil {
		t.Fatalf("got %v, want nil", d)
	}
}

func TestDropClosedKeepsNewestPerProcess(t *testing.T) {
	kid := &SessionNode{ID: "kid", Dir: "/a", Active: true}
	old := &SessionNode{ID: "old", Dir: "/a", Active: true, Updated: 1, Children: []*SessionNode{kid}}
	cur := &SessionNode{ID: "cur", Dir: "/a", Active: true, Updated: 2}
	gone := &SessionNode{ID: "gone", Dir: "/b", Active: true, Updated: 3}
	DropClosed([]*SessionNode{old, cur, gone}, map[string]int{"/a": 1})
	if old.Active || kid.Active || gone.Active || !cur.Active {
		t.Fatalf("old=%v kid=%v gone=%v cur=%v", old.Active, kid.Active, gone.Active, cur.Active)
	}
	keep := &SessionNode{Dir: "/z", Active: true}
	DropClosed([]*SessionNode{keep}, nil)
	if !keep.Active {
		t.Fatal("nil dirs must not change anything")
	}
}
