package fsnotify_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	contractDeadline = 5 * time.Second
	quietWindow      = 250 * time.Millisecond
)

func recursive(root string) string {
	return filepath.Join(root, "...")
}

func newContractWatcher(t *testing.T, paths ...string) *fsnotify.Watcher {
	t.Helper()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	for _, path := range paths {
		if err := w.Add(path); err != nil {
			t.Fatalf("Add(%q): %v", path, err)
		}
	}
	return w
}

func sortedPaths(paths []string) []string {
	got := slices.Clone(paths)
	sort.Strings(got)
	return got
}

func assertWatchList(t *testing.T, w *fsnotify.Watcher, want ...string) {
	t.Helper()

	got := sortedPaths(w.WatchList())
	want = sortedPaths(want)
	if !slices.Equal(got, want) {
		t.Fatalf("WatchList = %q; want %q", got, want)
	}
}

func waitForWatchList(t *testing.T, w *fsnotify.Watcher, want ...string) {
	t.Helper()

	want = sortedPaths(want)
	deadline := time.NewTimer(contractDeadline)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		got := sortedPaths(w.WatchList())
		if slices.Equal(got, want) {
			return
		}

		select {
		case err, ok := <-w.Errors:
			if ok && err != nil {
				t.Fatalf("watcher error while waiting for WatchList %q: %v", want, err)
			}
		case _, ok := <-w.Events:
			if !ok {
				t.Fatalf("Events closed while waiting for WatchList %q", want)
			}
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("WatchList = %q; want %q", got, want)
		}
	}
}

func waitForEvent(t *testing.T, w *fsnotify.Watcher, match func(fsnotify.Event) bool) fsnotify.Event {
	t.Helper()

	deadline := time.NewTimer(contractDeadline)
	defer deadline.Stop()

	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				t.Fatal("Events closed before the required event arrived")
			}
			if match(event) {
				return event
			}
		case err, ok := <-w.Errors:
			if ok && err != nil {
				t.Fatalf("watcher error: %v", err)
			}
		case <-deadline.C:
			t.Fatal("required event did not arrive before the contract deadline")
		}
	}
}

func waitForPathEvent(t *testing.T, w *fsnotify.Watcher, path string) fsnotify.Event {
	t.Helper()
	path = filepath.Clean(path)

	return waitForEvent(t, w, func(event fsnotify.Event) bool {
		return filepath.Clean(event.Name) == path
	})
}

func waitForAnyPathEvent(t *testing.T, w *fsnotify.Watcher, paths ...string) fsnotify.Event {
	t.Helper()

	cleaned := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		cleaned[filepath.Clean(path)] = struct{}{}
	}
	return waitForEvent(t, w, func(event fsnotify.Event) bool {
		_, ok := cleaned[filepath.Clean(event.Name)]
		return ok
	})
}

func assertNoPathEvent(t *testing.T, w *fsnotify.Watcher, path string) {
	t.Helper()
	path = filepath.Clean(path)

	deadline := time.NewTimer(quietWindow)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) == path {
				t.Fatalf("unexpected event for %q: %v", path, event)
			}
		case err, ok := <-w.Errors:
			if ok && err != nil {
				t.Fatalf("watcher error: %v", err)
			}
		case <-deadline.C:
			return
		}
	}
}

func assertSingleCreateEvent(t *testing.T, w *fsnotify.Watcher, path string) {
	t.Helper()
	path = filepath.Clean(path)

	if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}

	deadline := time.NewTimer(contractDeadline)
	defer deadline.Stop()
	var quiet *time.Timer
	var quietC <-chan time.Time
	creates := 0
	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				t.Fatal("Events closed before idempotent Add was observed")
			}
			if filepath.Clean(event.Name) != path || !event.Op.Has(fsnotify.Create) {
				continue
			}
			creates++
			if creates > 1 {
				t.Fatalf("one filesystem create produced %d Create events after repeated Add", creates)
			}
			if quiet == nil {
				quiet = time.NewTimer(quietWindow)
				defer quiet.Stop()
				quietC = quiet.C
			}
		case err, ok := <-w.Errors:
			if ok && err != nil {
				t.Fatalf("watcher error while checking idempotent Add: %v", err)
			}
		case <-quietC:
			if creates != 1 {
				t.Fatalf("Create events = %d; want 1", creates)
			}
			return
		case <-deadline.C:
			t.Fatalf("Create events = %d before contract deadline; want 1", creates)
		}
	}
}

func assertWatcherChannelsClosed(t *testing.T, w *fsnotify.Watcher) {
	t.Helper()

	deadline := time.NewTimer(contractDeadline)
	defer deadline.Stop()
	events := w.Events
	errors := w.Errors
	for events != nil || errors != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case _, ok := <-errors:
			if !ok {
				errors = nil
			}
		case <-deadline.C:
			t.Fatal("watcher channels did not close before the contract deadline")
		}
	}
}

func writeSentinel(t *testing.T, w *fsnotify.Watcher, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	waitForPathEvent(t, w, path)
}

func eventuallyWriteSentinel(t *testing.T, w *fsnotify.Watcher, dir string) {
	t.Helper()

	// A native directory event is not a public readiness barrier: a backend may
	// still be registering the discovered subtree when that event is delivered.
	// Probe with unique files until recursive coverage becomes observable.
	deadline := time.NewTimer(contractDeadline)
	defer deadline.Stop()
	probe := time.NewTicker(10 * time.Millisecond)
	defer probe.Stop()

	pending := make(map[string]struct{})
	attempts := 0
	writeProbe := func() {
		path := filepath.Join(dir, fmt.Sprintf(".fsnotify-recursive-contract-%d", attempts))
		attempts++
		if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
		pending[filepath.Clean(path)] = struct{}{}
	}
	writeProbe()

	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				t.Fatal("Events closed before recursive coverage became observable")
			}
			if _, matched := pending[filepath.Clean(event.Name)]; matched {
				return
			}
		case err, ok := <-w.Errors:
			if ok && err != nil {
				t.Fatalf("watcher error while probing recursive coverage: %v", err)
			}
		case <-probe.C:
			writeProbe()
		case <-deadline.C:
			t.Fatalf("recursive coverage did not become observable after %d probes", attempts)
		}
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir(%q): %v", path, err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func TestRecursiveContract(t *testing.T) {
	t.Run("add_existing_root", func(t *testing.T) {
		root := t.TempDir()
		mkdirAll(t, filepath.Join(root, "existing", "deep"))

		w := newContractWatcher(t, recursive(root))
		assertWatchList(t, w, root)
	})

	t.Run("register_existing_descendants", func(t *testing.T) {
		root := t.TempDir()
		dirs := []string{
			filepath.Join(root, "a"),
			filepath.Join(root, "a", "deep"),
			filepath.Join(root, "b"),
		}
		for _, dir := range dirs {
			mkdirAll(t, dir)
		}

		w := newContractWatcher(t, recursive(root))
		for i, dir := range dirs {
			writeSentinel(t, w, filepath.Join(dir, fmt.Sprintf("sentinel-%d", i)))
		}
	})

	t.Run("register_new_subtrees", func(t *testing.T) {
		root := t.TempDir()
		w := newContractWatcher(t, recursive(root))

		deep := filepath.Join(root, "new", "deep", "tree")
		mkdirAll(t, deep)
		eventuallyWriteSentinel(t, w, deep)
	})

	t.Run("reject_invalid_roots_atomically", func(t *testing.T) {
		root := t.TempDir()
		file := filepath.Join(root, "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		w := newContractWatcher(t, root)
		before := sortedPaths(w.WatchList())

		if err := w.Add(recursive(file)); err == nil {
			t.Fatal("recursive Add(file) succeeded")
		}
		if got := sortedPaths(w.WatchList()); !slices.Equal(got, before) {
			t.Fatalf("WatchList after Add(file) = %q; want %q", got, before)
		}

		missing := filepath.Join(root, "missing")
		if err := w.Add(recursive(missing)); err == nil {
			t.Fatal("recursive Add(missing) succeeded")
		}
		if got := sortedPaths(w.WatchList()); !slices.Equal(got, before) {
			t.Fatalf("WatchList after Add(missing) = %q; want %q", got, before)
		}

		writeSentinel(t, w, filepath.Join(root, "sentinel"))
	})

	t.Run("idempotent_add", func(t *testing.T) {
		root := t.TempDir()
		w := newContractWatcher(t)

		for range 3 {
			if err := w.Add(recursive(root)); err != nil {
				t.Fatalf("Add: %v", err)
			}
		}
		assertWatchList(t, w, root)
		assertSingleCreateEvent(t, w, filepath.Join(root, "sentinel"))
	})

	t.Run("watch_list_contains_only_explicit_watches", func(t *testing.T) {
		root := t.TempDir()
		mkdirAll(t, filepath.Join(root, "a", "b", "c"))

		w := newContractWatcher(t, recursive(root))
		assertWatchList(t, w, root)
	})

	t.Run("internal_subwatches_are_hidden", func(t *testing.T) {
		root := t.TempDir()
		oldPath := filepath.Join(root, "old")
		newPath := filepath.Join(root, "new")
		mkdirAll(t, filepath.Join(oldPath, "deep"))

		w := newContractWatcher(t, recursive(root))
		grown := filepath.Join(root, "grown", "deep")
		mkdirAll(t, grown)
		eventuallyWriteSentinel(t, w, grown)
		assertWatchList(t, w, root)

		if err := os.Rename(oldPath, newPath); err != nil {
			t.Fatal(err)
		}
		eventuallyWriteSentinel(t, w, filepath.Join(newPath, "deep"))
		assertWatchList(t, w, root)
	})

	t.Run("overlapping_roots_are_independent", func(t *testing.T) {
		outer := t.TempDir()
		inner := filepath.Join(outer, "inner")
		mkdirAll(t, filepath.Join(inner, "deep"))

		w := newContractWatcher(t, recursive(outer), recursive(inner))
		assertWatchList(t, w, inner, outer)

		if err := w.Remove(outer); err != nil {
			t.Fatalf("Remove(outer): %v", err)
		}
		assertWatchList(t, w, inner)

		deeper := filepath.Join(inner, "new", "deep")
		mkdirAll(t, deeper)
		eventuallyWriteSentinel(t, w, deeper)

		if err := w.Add(recursive(outer)); err != nil {
			t.Fatalf("Add(outer): %v", err)
		}
		if err := w.Remove(inner); err != nil {
			t.Fatalf("Remove(inner): %v", err)
		}
		assertWatchList(t, w, outer)
		outerOwned := filepath.Join(inner, "outer-owned")
		mkdir(t, outerOwned)
		eventuallyWriteSentinel(t, w, outerOwned)
	})

	t.Run("explicit_child_survives_outer_remove", func(t *testing.T) {
		outer := t.TempDir()
		child := filepath.Join(outer, "child")
		mkdir(t, child)

		w := newContractWatcher(t, recursive(outer), child)
		if err := w.Remove(recursive(outer)); err != nil {
			t.Fatalf("Remove(outer): %v", err)
		}
		assertWatchList(t, w, child)
		writeSentinel(t, w, filepath.Join(child, "sentinel"))
	})

	t.Run("inner_root_survives_outer_remove", func(t *testing.T) {
		outer := t.TempDir()
		inner := filepath.Join(outer, "inner")
		mkdir(t, inner)

		w := newContractWatcher(t, recursive(outer), recursive(inner))
		if err := w.Remove(outer); err != nil {
			t.Fatalf("Remove(outer): %v", err)
		}
		assertWatchList(t, w, inner)

		deep := filepath.Join(inner, "new", "deep")
		mkdirAll(t, deep)
		eventuallyWriteSentinel(t, w, deep)
	})

	t.Run("remove_forms_are_equivalent", func(t *testing.T) {
		root := t.TempDir()
		w := newContractWatcher(t, recursive(root))

		if err := w.Remove(recursive(root)); err != nil {
			t.Fatalf("Remove(recursive(root)): %v", err)
		}
		assertWatchList(t, w)

		if err := w.Add(recursive(root)); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if err := w.Remove(root); err != nil {
			t.Fatalf("Remove(root): %v", err)
		}
		assertWatchList(t, w)
	})

	t.Run("remove_releases_only_exclusive_coverage", func(t *testing.T) {
		root := t.TempDir()
		shared := filepath.Join(root, "shared")
		exclusive := filepath.Join(root, "exclusive")
		mkdir(t, shared)
		mkdir(t, exclusive)

		w := newContractWatcher(t, recursive(root), shared)
		if err := w.Remove(root); err != nil {
			t.Fatalf("Remove(root): %v", err)
		}
		assertWatchList(t, w, shared)

		writeSentinel(t, w, filepath.Join(shared, "kept"))
		unwatched := filepath.Join(exclusive, "not-watched")
		if err := os.WriteFile(unwatched, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		assertNoPathEvent(t, w, unwatched)
	})

	t.Run("root_disappearance_cleans_state", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "root")
		mkdirAll(t, filepath.Join(root, "child"))

		w := newContractWatcher(t, recursive(root))
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		waitForWatchList(t, w)

		if err := w.Remove(root); !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			t.Fatalf("Remove(root) = %T %v; want ErrNonExistentWatch", err, err)
		}
	})

	t.Run("rename_within_tree_preserves_coverage", func(t *testing.T) {
		root := t.TempDir()
		oldPath := filepath.Join(root, "old")
		newPath := filepath.Join(root, "new")
		mkdirAll(t, filepath.Join(oldPath, "deep"))

		w := newContractWatcher(t, recursive(root))
		if err := os.Rename(oldPath, newPath); err != nil {
			t.Fatal(err)
		}
		eventuallyWriteSentinel(t, w, filepath.Join(newPath, "deep"))
	})

	t.Run("move_out_releases_and_replacement_registers", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "root")
		moved := filepath.Join(parent, "moved")
		oldPath := filepath.Join(root, "subtree")
		mkdirAll(t, filepath.Join(oldPath, "deep"))

		w := newContractWatcher(t, recursive(root))
		if err := os.Rename(oldPath, moved); err != nil {
			t.Fatal(err)
		}
		waitForAnyPathEvent(t, w, oldPath, moved)

		outside := filepath.Join(moved, "outside")
		if err := os.WriteFile(outside, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		assertNoPathEvent(t, w, outside)

		replacement := filepath.Join(oldPath, "replacement")
		mkdirAll(t, replacement)
		eventuallyWriteSentinel(t, w, replacement)
	})

	t.Run("move_in_registers_complete_subtree", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "root")
		source := filepath.Join(parent, "source")
		target := filepath.Join(root, "target")
		deep := filepath.Join(source, "existing", "deep")
		mkdir(t, root)
		mkdirAll(t, deep)

		w := newContractWatcher(t, recursive(root))
		if err := os.Rename(source, target); err != nil {
			t.Fatal(err)
		}
		eventuallyWriteSentinel(t, w, filepath.Join(target, "existing", "deep"))
	})

	t.Run("reused_path_is_new_object", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "object")
		mkdir(t, path)

		w := newContractWatcher(t, recursive(root))
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		waitForPathEvent(t, w, path)

		mkdir(t, path)
		eventuallyWriteSentinel(t, w, path)
	})

	t.Run("concurrent_lifecycle", func(t *testing.T) {
		root := t.TempDir()
		w, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Add(recursive(root)); err != nil {
			t.Fatal(err)
		}

		const workers = 8
		start := make(chan struct{})
		ready := make(chan struct{}, workers+1)
		proceed := make(chan struct{})
		errs := make(chan error, workers+16)
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if err := w.Add(recursive(root)); err != nil {
					errs <- err
					ready <- struct{}{}
					return
				}
				ready <- struct{}{}
				<-proceed
				for range 25 {
					if err := w.Add(recursive(root)); err != nil && !errors.Is(err, fsnotify.ErrClosed) {
						errs <- err
						return
					}
					_ = w.WatchList()
					if err := w.Remove(root); err != nil &&
						!errors.Is(err, fsnotify.ErrNonExistentWatch) &&
						!errors.Is(err, fsnotify.ErrClosed) {
						errs <- err
						return
					}
				}
			}()
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ready <- struct{}{}
			<-proceed
			for i := range 100 {
				err := os.WriteFile(
					filepath.Join(root, fmt.Sprintf("event-%d", i)),
					nil,
					0o600,
				)
				if err != nil {
					errs <- err
					return
				}
			}
		}()

		drained := make(chan struct{})
		go func() {
			defer close(drained)
			events := w.Events
			watchErrors := w.Errors
			for events != nil || watchErrors != nil {
				select {
				case _, ok := <-events:
					if !ok {
						events = nil
					}
				case err, ok := <-watchErrors:
					if !ok {
						watchErrors = nil
						continue
					}
					if err != nil && !errors.Is(err, fsnotify.ErrClosed) {
						errs <- err
					}
				}
			}
		}()

		close(start)
		for range workers + 1 {
			<-ready
		}
		close(proceed)
		closeDone := make(chan error, 1)
		go func() { closeDone <- w.Close() }()

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(contractDeadline):
			t.Fatal("concurrent Add/Remove/WatchList/event delivery did not complete")
		}
		select {
		case err := <-closeDone:
			if err != nil {
				t.Fatalf("Close: %v", err)
			}
		case <-time.After(contractDeadline):
			t.Fatal("Close did not complete during concurrent lifecycle operations")
		}
		select {
		case <-drained:
		case <-time.After(contractDeadline):
			t.Fatal("event consumer did not observe channel termination")
		}
		close(errs)
		for err := range errs {
			t.Errorf("concurrent lifecycle error: %v", err)
		}
	})

	t.Run("idempotent_close", func(t *testing.T) {
		root := t.TempDir()
		w, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Add(recursive(root)); err != nil {
			t.Fatal(err)
		}

		for i := range 3 {
			done := make(chan error, 1)
			go func() { done <- w.Close() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Close call %d: %v", i+1, err)
				}
			case <-time.After(contractDeadline):
				t.Fatalf("Close call %d did not complete", i+1)
			}
		}
		assertWatcherChannelsClosed(t, w)
	})

	t.Run("backpressure_does_not_deadlock_close", func(t *testing.T) {
		root := t.TempDir()
		w, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Add(recursive(root)); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(root, "blocked"), nil, 0o600); err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() { done <- w.Close() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Close: %v", err)
			}
		case <-time.After(contractDeadline):
			t.Fatal("Close deadlocked while Events was backpressured")
		}
	})

	t.Run("internal_management_is_silent", func(t *testing.T) {
		root := t.TempDir()
		mkdirAll(t, filepath.Join(root, "a", "b", "c"))
		w := newContractWatcher(t, recursive(root))

		deadline := time.NewTimer(quietWindow)
		defer deadline.Stop()
		select {
		case event := <-w.Events:
			t.Fatalf("internal recursive registration emitted %v", event)
		case err := <-w.Errors:
			t.Fatalf("internal recursive registration emitted error %v", err)
		case <-deadline.C:
		}
	})

	t.Run("error_classes_and_state_effects", func(t *testing.T) {
		root := t.TempDir()
		w, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}

		if err := w.Remove(root); !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			t.Fatalf("Remove(unwatched) = %T %v; want ErrNonExistentWatch", err, err)
		}
		if err := w.Add(recursive(root)); err != nil {
			t.Fatal(err)
		}
		assertWatchList(t, w, root)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := w.Add(recursive(root)); !errors.Is(err, fsnotify.ErrClosed) {
			t.Fatalf("Add after Close = %T %v; want ErrClosed", err, err)
		}
		if err := w.Remove(root); err != nil {
			t.Fatalf("Remove after Close = %v; want nil", err)
		}
		if got := w.WatchList(); got != nil {
			t.Fatalf("WatchList after Close = %q; want nil", got)
		}
	})

	t.Run("prefix_similar_roots_are_independent", func(t *testing.T) {
		parent := t.TempDir()
		a := filepath.Join(parent, "a")
		ab := filepath.Join(parent, "ab")
		mkdir(t, a)
		mkdir(t, ab)

		w := newContractWatcher(t, recursive(a), recursive(ab))
		if err := w.Remove(a); err != nil {
			t.Fatal(err)
		}
		assertWatchList(t, w, ab)
		writeSentinel(t, w, filepath.Join(ab, "sentinel"))
	})

	t.Run("prefix_similar_root_rename_is_isolated", func(t *testing.T) {
		parent := t.TempDir()
		a := filepath.Join(parent, "a")
		ab := filepath.Join(parent, "ab")
		renamed := filepath.Join(parent, "renamed")
		mkdir(t, a)
		mkdir(t, ab)

		w := newContractWatcher(t, recursive(a), recursive(ab))
		if err := os.Rename(a, renamed); err != nil {
			t.Fatal(err)
		}
		waitForWatchList(t, w, ab)
		writeSentinel(t, w, filepath.Join(ab, "sentinel"))
	})

	t.Run("non_recursive_compatibility", func(t *testing.T) {
		root := t.TempDir()
		child := filepath.Join(root, "child")
		mkdir(t, child)

		w := newContractWatcher(t, root)
		assertWatchList(t, w, root)

		writeSentinel(t, w, filepath.Join(root, "direct"))
		deep := filepath.Join(child, "deep")
		if err := os.WriteFile(deep, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		assertNoPathEvent(t, w, deep)

		if err := w.Remove(root); err != nil {
			t.Fatal(err)
		}
		assertWatchList(t, w)
	})
}

func TestRecursiveContractResourceInvariants(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, "a", "b", "c"))

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(recursive(file)); err == nil {
		t.Fatal("recursive Add(file) succeeded")
	}
	if err := w.Add(recursive(filepath.Join(root, "missing"))); err == nil {
		t.Fatal("recursive Add(missing) succeeded")
	}
	assertWatchList(t, w)

	if err := w.Add(recursive(root)); err != nil {
		t.Fatal(err)
	}
	assertWatchList(t, w, root)

	if err := w.Remove(root); err != nil {
		t.Fatal(err)
	}
	assertWatchList(t, w)

	if err := w.Add(recursive(root)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := w.WatchList(); got != nil {
		t.Fatalf("WatchList after Close = %q; want nil", got)
	}
	assertWatcherChannelsClosed(t, w)
}
