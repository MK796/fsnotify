//go:build solaris

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"fmt"
	"os"
	"reflect"
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
	fen := watcher.b.(*fen)

	fen.mu.Lock()
	beforeDirs := cloneOpMap(fen.dirs)
	beforeWatches := cloneOpMap(fen.watches)
	beforeByUser := cloneOpMap(fen.byUser)
	beforeRecurse := cloneOpMap(fen.recurse)
	beforeOwners := cloneOwnerMap(fen.owners)
	beforeInfo := cloneInfoMap(fen.info)
	fen.mu.Unlock()
	paths := []string{tmp, root, allowed, denied}
	beforeAssociations := make(map[string]bool, len(paths))
	for _, path := range paths {
		beforeAssociations[path] = fen.port.PathIsWatched(path)
	}

	if err := watcher.Add(join(root, "...")); err == nil {
		t.Fatal("recursive Add succeeded with an unreadable subtree")
	}

	fen.mu.Lock()
	if !reflect.DeepEqual(fen.dirs, beforeDirs) {
		t.Errorf("directories after rollback = %#v; want %#v", fen.dirs, beforeDirs)
	}
	if !reflect.DeepEqual(fen.watches, beforeWatches) {
		t.Errorf("watches after rollback = %#v; want %#v", fen.watches, beforeWatches)
	}
	if !reflect.DeepEqual(fen.byUser, beforeByUser) {
		t.Errorf("user watches after rollback = %#v; want %#v", fen.byUser, beforeByUser)
	}
	if !reflect.DeepEqual(fen.recurse, beforeRecurse) {
		t.Errorf("recursive roots after rollback = %#v; want %#v", fen.recurse, beforeRecurse)
	}
	if !reflect.DeepEqual(fen.owners, beforeOwners) {
		t.Errorf("owners after rollback = %#v; want %#v", fen.owners, beforeOwners)
	}
	if !sameInfoMap(fen.info, beforeInfo) {
		t.Errorf("file identities after rollback differ from their pre-call state")
	}
	fen.mu.Unlock()

	for path, want := range beforeAssociations {
		if got := fen.port.PathIsWatched(path); got != want {
			t.Errorf("PathIsWatched(%q) after rollback = %t; want %t", path, got, want)
		}
	}
}

func cloneOpMap(source map[string]Op) map[string]Op {
	clone := make(map[string]Op, len(source))
	for path, op := range source {
		clone[path] = op
	}
	return clone
}

func cloneOwnerMap(source map[string]map[string]struct{}) map[string]map[string]struct{} {
	clone := make(map[string]map[string]struct{}, len(source))
	for path, owners := range source {
		ownerClone := make(map[string]struct{}, len(owners))
		for owner := range owners {
			ownerClone[owner] = struct{}{}
		}
		clone[path] = ownerClone
	}
	return clone
}

func cloneInfoMap(source map[string]os.FileInfo) map[string]os.FileInfo {
	clone := make(map[string]os.FileInfo, len(source))
	for path, info := range source {
		clone[path] = info
	}
	return clone
}

func sameInfoMap(have, want map[string]os.FileInfo) bool {
	if len(have) != len(want) {
		return false
	}
	for path, wantInfo := range want {
		haveInfo := have[path]
		if haveInfo == nil || !os.SameFile(haveInfo, wantInfo) {
			return false
		}
	}
	return true
}
