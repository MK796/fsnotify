//go:build solaris

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
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
	if got := watcher.WatchList(); len(got) != 1 || got[0] != tmp {
		t.Fatalf("WatchList after failed recursive Add: %q; want only %q", got, tmp)
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

func TestFenRecursiveExistingDescendantCoverage(t *testing.T) {
	root := t.TempDir()
	dirs := []string{
		join(root, "a"),
		join(root, "a", "deep"),
		join(root, "b"),
	}
	for _, dir := range dirs {
		mkdirAll(t, dir)
	}

	watcher := newWatcher(t, join(root, "..."))
	defer watcher.Close()
	fen := watcher.b.(*fen)

	for i, dir := range dirs {
		path := join(dir, fmt.Sprintf("sentinel-%d", i))
		if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
		waitForFenPathEvent(t, watcher, path, fen, append([]string{root}, dirs...)...)
	}
}

func TestFenRecursiveRenameCoverage(t *testing.T) {
	root := t.TempDir()
	oldPath := join(root, "old")
	oldDeep := join(oldPath, "deep")
	newPath := join(root, "new")
	newDeep := join(newPath, "deep")
	mkdirAll(t, oldDeep)

	watcher := newWatcher(t, join(root, "..."))
	defer watcher.Close()
	fen := watcher.b.(*fen)

	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	probe := time.NewTicker(10 * time.Millisecond)
	defer probe.Stop()

	pending := make(map[string]struct{})
	events := make([]Event, 0, 16)
	attempts := 0
	writeProbe := func() {
		path := join(newDeep, fmt.Sprintf("sentinel-%d", attempts))
		attempts++
		if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
		pending[filepath.Clean(path)] = struct{}{}
	}
	writeProbe()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				t.Fatal("Events closed before recursive rename coverage became observable")
			}
			events = append(events, event)
			if _, ok := pending[filepath.Clean(event.Name)]; ok {
				return
			}
		case err, ok := <-watcher.Errors:
			if ok && err != nil {
				t.Fatalf("watcher error: %v", err)
			}
		case <-probe.C:
			writeProbe()
		case <-deadline.C:
			t.Fatalf(
				"recursive rename coverage missing after %d probes; events=%v; state:\n%s",
				attempts,
				events,
				fenState(fen, root, oldPath, oldDeep, newPath, newDeep),
			)
		}
	}
}

func TestFenCloseWhileGetBlocked(t *testing.T) {
	for range 100 {
		watcher, err := NewWatcher()
		if err != nil {
			t.Fatal(err)
		}

		errorsDone := make(chan []error, 1)
		go func() {
			var errors []error
			for err := range watcher.Errors {
				if err != nil {
					errors = append(errors, err)
				}
			}
			errorsDone <- errors
		}()

		if err := watcher.Close(); err != nil {
			t.Fatal(err)
		}

		select {
		case errors := <-errorsDone:
			if len(errors) > 0 {
				t.Fatalf("errors after Close: %v", errors)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Errors did not close after Close")
		}
		for range watcher.Events {
		}
	}
}

func waitForFenPathEvent(t *testing.T, watcher *Watcher, path string, fen *fen, statePaths ...string) {
	t.Helper()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	events := make([]Event, 0, 8)
	want := filepath.Clean(path)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				t.Fatalf("Events closed while waiting for %q", path)
			}
			events = append(events, event)
			if filepath.Clean(event.Name) == want {
				return
			}
		case err, ok := <-watcher.Errors:
			if ok && err != nil {
				t.Fatalf("watcher error while waiting for %q: %v", path, err)
			}
		case <-deadline.C:
			t.Fatalf(
				"event for %q missing; events=%v; state:\n%s",
				path,
				events,
				fenState(fen, statePaths...),
			)
		}
	}
}

func fenState(w *fen, paths ...string) string {
	type pathState struct {
		path      string
		directory bool
		file      bool
		user      bool
		recursive bool
		identity  bool
		owners    []string
	}

	w.mu.Lock()
	states := make([]pathState, 0, len(paths))
	for _, path := range paths {
		_, directory := w.dirs[path]
		_, file := w.watches[path]
		_, user := w.byUser[path]
		_, recursive := w.recurse[path]
		owners := make([]string, 0, len(w.owners[path]))
		for owner := range w.owners[path] {
			owners = append(owners, owner)
		}
		sort.Strings(owners)
		states = append(states, pathState{
			path:      path,
			directory: directory,
			file:      file,
			user:      user,
			recursive: recursive,
			identity:  w.info[path] != nil,
			owners:    owners,
		})
	}
	w.mu.Unlock()

	var out strings.Builder
	for _, state := range states {
		fmt.Fprintf(
			&out,
			"%q: dir=%t file=%t user=%t recursive=%t identity=%t owners=%q associated=%t\n",
			state.path,
			state.directory,
			state.file,
			state.user,
			state.recursive,
			state.identity,
			state.owners,
			w.port.PathIsWatched(state.path),
		)
	}
	if pending, err := w.port.Pending(); err != nil {
		fmt.Fprintf(&out, "pending: error=%v\n", err)
	} else {
		fmt.Fprintf(&out, "pending: %d\n", pending)
	}
	return out.String()
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
