//go:build freebsd || openbsd || netbsd || dragonfly || darwin

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
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

func TestRecursiveRemoveState(t *testing.T) {
	type watchState struct {
		wd, path, byDir, seen, byUser, recurse int
	}

	var (
		tmp       = t.TempDir()
		nested    = join(tmp, "a", "b")
		other     = join(tmp, "c")
		recurse   = join(tmp, "...")
		wantPaths = []string{tmp, join(tmp, "a"), nested, other}
	)
	mkdirAll(t, nested)
	mkdir(t, other)
	touch(t, tmp, "root-file")
	touch(t, nested, "nested-file")
	touch(t, other, "other-file")
	slices.Sort(wantPaths)

	w := newWatcher(t)
	defer w.Close()
	kq := w.b.(*kqueue)

	for i := range 32 {
		if err := w.Add(recurse); err != nil {
			t.Fatalf("cycle %d: Add(%q): %v", i, recurse, err)
		}

		havePaths := w.WatchList()
		slices.Sort(havePaths)
		if !slices.Equal(havePaths, wantPaths) {
			t.Fatalf("cycle %d: unexpected watch list\nhave: %q\nwant: %q",
				i, havePaths, wantPaths)
		}

		kq.watches.mu.RLock()
		fds := make([]int, 0, len(kq.watches.wd))
		for fd := range kq.watches.wd {
			fds = append(fds, fd)
		}
		state := watchState{
			len(kq.watches.wd),
			len(kq.watches.path),
			len(kq.watches.byDir),
			len(kq.watches.seen),
			len(kq.watches.byUser),
			len(kq.watches.recurse),
		}
		kq.watches.mu.RUnlock()

		if state != (watchState{7, 7, 5, 6, 1, 1}) {
			t.Fatalf("cycle %d: unexpected recursive state: %v", i, state)
		}

		if err := w.Remove(tmp); err != nil {
			t.Fatalf("cycle %d: Remove(%q): %v", i, tmp, err)
		}

		kq.watches.mu.RLock()
		state = watchState{
			len(kq.watches.wd),
			len(kq.watches.path),
			len(kq.watches.byDir),
			len(kq.watches.seen),
			len(kq.watches.byUser),
			len(kq.watches.recurse),
		}
		kq.watches.mu.RUnlock()
		if state != (watchState{}) {
			t.Fatalf("cycle %d: recursive state not empty after Remove: %v", i, state)
		}

		for _, fd := range fds {
			var stat unix.Stat_t
			if err := unix.Fstat(fd, &stat); !errors.Is(err, unix.EBADF) {
				t.Fatalf("cycle %d: descriptor %d still open after Remove: %v", i, fd, err)
			}
		}
	}
}

func TestRecursiveAddFailureState(t *testing.T) {
	type watchState struct {
		wd, path, byDir, seen, byUser, recurse []string
	}
	snapshot := func(kq *kqueue) watchState {
		kq.watches.mu.RLock()
		defer kq.watches.mu.RUnlock()

		state := watchState{}
		for fd, info := range kq.watches.wd {
			state.wd = append(state.wd,
				fmt.Sprintf("%d:%s:%s:%t:%d", fd, info.name, info.linkName, info.isDir, info.dirFlags))
		}
		for path, fd := range kq.watches.path {
			state.path = append(state.path, fmt.Sprintf("%s:%d", path, fd))
		}
		for path, fds := range kq.watches.byDir {
			for fd := range fds {
				state.byDir = append(state.byDir, fmt.Sprintf("%s:%d", path, fd))
			}
		}
		for path := range kq.watches.seen {
			state.seen = append(state.seen, path)
		}
		for path := range kq.watches.byUser {
			state.byUser = append(state.byUser, path)
		}
		for path, op := range kq.watches.recurse {
			state.recurse = append(state.recurse, fmt.Sprintf("%s:%d", path, op))
		}
		slices.Sort(state.wd)
		slices.Sort(state.path)
		slices.Sort(state.byDir)
		slices.Sort(state.seen)
		slices.Sort(state.byUser)
		slices.Sort(state.recurse)
		return state
	}
	equal := func(a, b watchState) bool {
		return slices.Equal(a.wd, b.wd) &&
			slices.Equal(a.path, b.path) &&
			slices.Equal(a.byDir, b.byDir) &&
			slices.Equal(a.seen, b.seen) &&
			slices.Equal(a.byUser, b.byUser) &&
			slices.Equal(a.recurse, b.recurse)
	}

	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			var (
				tmp        = t.TempDir()
				accessible = join(tmp, "a", "b")
				denied     = join(tmp, "z-denied")
				recurse    = join(tmp, "...")
			)
			mkdirAll(t, accessible)
			touch(t, accessible, "file")
			mkdir(t, denied)
			touch(t, denied, "file")
			if err := os.Chmod(denied, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(denied, 0o755); err != nil {
					t.Error(err)
				}
			})

			if _, err := os.ReadDir(denied); err == nil {
				t.Skip("test requires an unreadable directory")
			}

			w := newWatcher(t)
			defer w.Close()
			kq := w.b.(*kqueue)

			if existing {
				if err := w.Add(tmp); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshot(kq)

			if err := w.Add(recurse); err == nil {
				t.Fatalf("Add(%q) succeeded for an unreadable tree", recurse)
			}

			after := snapshot(kq)
			if !equal(before, after) {
				t.Fatalf("recursive state changed after failed Add:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}
