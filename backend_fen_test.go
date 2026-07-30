//go:build solaris

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRemoveState(t *testing.T) {
	var (
		tmp  = t.TempDir()
		dir  = join(tmp, "dir")
		file = join(dir, "file")
	)
	mkdir(t, dir)
	touch(t, file)

	w := newWatcher(t, tmp)
	addWatch(t, w, tmp)
	addWatch(t, w, file)

	check := func(wantDirs, wantFiles int) {
		t.Helper()
		if len(w.b.(*fen).watches) != wantFiles {
			var d []string
			for k, v := range w.b.(*fen).watches {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches (have %d, want %d):\n%v",
				len(w.b.(*fen).watches), wantFiles, strings.Join(d, "\n"))
		}
		if len(w.b.(*fen).dirs) != wantDirs {
			var d []string
			for k, v := range w.b.(*fen).dirs {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.dirs (have %d, want %d):\n%v",
				len(w.b.(*fen).dirs), wantDirs, strings.Join(d, "\n"))
		}
	}

	check(1, 1)

	// Shouldn't change internal state.
	if err := w.Add("/path-doesnt-exist"); err == nil {
		t.Fatal(err)
	}
	check(1, 1)

	if err := w.Remove(file); err != nil {
		t.Fatal(err)
	}
	check(1, 0)

	if err := w.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
}

func TestFenRecursiveAddRollback(t *testing.T) {
	tmp := t.TempDir()
	root := join(tmp, "root")
	allowed := join(root, "a-allowed")
	denied := join(root, "z-denied")
	mkdir(t, root)
	mkdir(t, allowed)
	mkdir(t, denied)
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(denied, 0o755)
	})

	watcher := newWatcher(t, tmp)
	defer watcher.Close()
	if err := watcher.Add(join(root, "...")); err == nil {
		t.Fatal("recursive Add succeeded with an unreadable subtree")
	}

	if got := watcher.WatchList(); len(got) != 1 || got[0] != tmp {
		t.Fatalf("WatchList after failed recursive Add: %q; want only %q", got, tmp)
	}

	fen := watcher.b.(*fen)
	fen.mu.Lock()
	defer fen.mu.Unlock()
	for path := range fen.dirs {
		if path == root || hasPathPrefix(path, root) {
			t.Errorf("directory retained after failed recursive Add: %q", path)
		}
	}
	for path := range fen.watches {
		if path == root || hasPathPrefix(path, root) {
			t.Errorf("watch retained after failed recursive Add: %q", path)
		}
	}
	for path := range fen.owners {
		if path == root || hasPathPrefix(path, root) {
			t.Errorf("owner state retained after failed recursive Add: %q", path)
		}
	}
	for path := range fen.info {
		if path == root || hasPathPrefix(path, root) {
			t.Errorf("identity retained after failed recursive Add: %q", path)
		}
	}
	if _, ok := fen.byUser[root]; ok {
		t.Errorf("user watch retained after failed recursive Add: %q", root)
	}
	if _, ok := fen.recurse[root]; ok {
		t.Errorf("recursive root retained after failed recursive Add: %q", root)
	}
}
