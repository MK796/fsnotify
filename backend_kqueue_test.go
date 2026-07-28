//go:build freebsd || openbsd || netbsd || dragonfly || darwin

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
	kq := w.b.(*kqueue)
	addWatch(t, w, tmp)
	addWatch(t, w, file)

	check := func(wantUser, wantTotal int) {
		t.Helper()

		if len(kq.watches.path) != wantTotal {
			var d []string
			for k, v := range kq.watches.path {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.path (have %d, want %d):\n%v",
				len(kq.watches.path), wantTotal, strings.Join(d, "\n"))
		}
		if len(kq.watches.wd) != wantTotal {
			var d []string
			for k, v := range kq.watches.wd {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.wd (have %d, want %d):\n%v",
				len(kq.watches.wd), wantTotal, strings.Join(d, "\n"))
		}
		if len(kq.watches.byUser) != wantUser {
			var d []string
			for k, v := range kq.watches.byUser {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.byUser (have %d, want %d):\n%v",
				len(kq.watches.byUser), wantUser, strings.Join(d, "\n"))
		}
	}

	check(2, 3)

	// Shouldn't change internal state.
	if err := w.Add("/path-doesnt-exist"); err == nil {
		t.Fatal(err)
	}
	check(2, 3)

	if err := w.Remove(file); err != nil {
		t.Fatal(err)
	}
	check(1, 2)

	if err := w.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	check(0, 0)

	// Don't check these after ever remove since they don't map easily to number
	// of files watches. Just make sure they're 0 after everything is removed.
	{
		want := 0
		if len(kq.watches.byDir) != want {
			var d []string
			for k, v := range kq.watches.byDir {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.byDir (have %d, want %d):\n%v",
				len(kq.watches.byDir), want, strings.Join(d, "\n"))
		}

		if len(kq.watches.seen) != want {
			var d []string
			for k, v := range kq.watches.seen {
				d = append(d, fmt.Sprintf("%#v = %#v", k, v))
			}
			t.Errorf("unexpected number of entries in w.watches.seen (have %d, want %d):\n%v",
				len(kq.watches.seen), want, strings.Join(d, "\n"))
			return
		}
	}

	// Make sure Close() cleans up everything.
	addWatch(t, w, tmp)
	addWatch(t, w, file)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
}

func TestRecursiveMoveOutDropsInternalWatches(t *testing.T) {
	tmp := t.TempDir()
	root := join(tmp, "root")
	child := join(root, "child")
	mkdir(t, root)
	mkdir(t, child)
	mkdir(t, child, "nested")
	touch(t, child, "nested", "file")

	collector := newCollector(t, join(root, "..."))
	collector.collect(t)
	kq := collector.w.b.(*kqueue)

	if err := os.Rename(child, join(tmp, "moved")); err != nil {
		t.Fatal(err)
	}
	waitForEvents()

	kq.watches.mu.RLock()
	for path := range kq.watches.path {
		if path == child || hasPathPrefix(path, child) {
			t.Errorf("stale watch after moving directory out of recursive root: %q", path)
		}
	}
	kq.watches.mu.RUnlock()

	collector.stop(t)
}

func TestRecursiveAddRollback(t *testing.T) {
	tmp := t.TempDir()
	root := join(tmp, "root")
	allowed := join(root, "a-allowed")
	denied := join(root, "z-denied")
	mkdir(t, root)
	mkdir(t, allowed)
	touch(t, allowed, "file")
	mkdir(t, denied)
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(denied, 0o755)
	})

	w := newWatcher(t)
	if err := w.Add(join(root, "...")); err == nil {
		t.Fatal("recursive Add succeeded with an unreadable subtree")
	}

	kq := w.b.(*kqueue)
	if got := w.WatchList(); len(got) != 0 {
		t.Fatalf("WatchList after failed recursive Add: %q", got)
	}

	kq.watches.mu.RLock()
	defer kq.watches.mu.RUnlock()
	if len(kq.watches.path) != 0 ||
		len(kq.watches.wd) != 0 ||
		len(kq.watches.byDir) != 0 ||
		len(kq.watches.byUser) != 0 ||
		len(kq.watches.target) != 0 ||
		len(kq.watches.recurse) != 0 ||
		len(kq.watches.owners) != 0 ||
		len(kq.watches.seen) != 0 {
		t.Fatalf("watch state retained after failed recursive Add: paths=%d descriptors=%d dirs=%d users=%d targets=%d roots=%d owners=%d seen=%d",
			len(kq.watches.path),
			len(kq.watches.wd),
			len(kq.watches.byDir),
			len(kq.watches.byUser),
			len(kq.watches.target),
			len(kq.watches.recurse),
			len(kq.watches.owners),
			len(kq.watches.seen),
		)
	}
}
