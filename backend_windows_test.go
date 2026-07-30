//go:build windows

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func (w *readDirChangesW) testCloseState() string {
	w.mu.Lock()
	closed := w.closed
	port := w.port
	watchCount := 0
	for _, index := range w.watches {
		watchCount += len(index)
	}
	w.mu.Unlock()

	doneClosed := false
	select {
	case <-w.done:
		doneClosed = true
	default:
	}
	closeDone := false
	select {
	case <-w.closeDone:
		closeDone = true
	default:
	}

	return fmt.Sprintf(
		"closed=%t done_closed=%t close_request_queued=%d close_done=%t port=%d watches=%d",
		closed,
		doneClosed,
		len(w.closeRequest),
		closeDone,
		port,
		watchCount,
	)
}

func TestRemoveState(t *testing.T) {
	var (
		tmp  = t.TempDir()
		dir  = join(tmp, "dir")
		file = join(dir, "file")
	)
	mkdir(t, dir)
	touch(t, file)

	w := newWatcher(t)
	backend := w.b.(*readDirChangesW)
	addWatch(t, w, file)
	addWatch(t, w, tmp)

	check := func(want ...string) {
		t.Helper()
		have := w.WatchList()
		slices.Sort(have)
		slices.Sort(want)
		if !slices.Equal(have, want) {
			t.Fatalf("WatchList() = %q; want %q", have, want)
		}
	}

	check(file, tmp)

	// Shouldn't change internal state.
	if err := w.Add(join(tmp, "path-doesnt-exist")); err == nil {
		t.Fatal("Add() succeeded for a path that does not exist")
	}
	check(file, tmp)

	if err := w.Remove(file); err != nil {
		t.Fatal(err)
	}
	check(tmp)

	if err := w.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	check()

	// Re-adding while the asynchronous removal completes must remain safe.
	addWatch(t, w, tmp)
	addWatch(t, w, file)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	check()

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.watches) != 0 {
		t.Errorf("watch map contains %d volume entries after Close", len(backend.watches))
	}
	if len(backend.pending) != 0 {
		t.Errorf("pending map contains %d operations after Close", len(backend.pending))
	}
	if backend.port != windows.InvalidHandle {
		t.Errorf("completion port is still valid after Close: %v", backend.port)
	}
}

func TestWindowsRemWatch(t *testing.T) {
	tmp := t.TempDir()

	touch(t, tmp, "file")

	w := newWatcher(t)
	defer w.Close()

	addWatch(t, w, tmp)
	if err := w.Remove(tmp); err != nil {
		t.Fatalf("Could not remove the watch: %v", err)
	}
	if err := w.b.(*readDirChangesW).remWatch(tmp); err == nil {
		t.Fatal("Should be fail with closed handle")
	}
}

func TestWindowsIOLifecycle(t *testing.T) {
	assertClosed := func(t *testing.T, backend *readDirChangesW, tracked []*watch) {
		t.Helper()

		backend.mu.Lock()
		defer backend.mu.Unlock()
		if len(backend.watches) != 0 {
			t.Errorf("watch map contains %d volume entries after Close", len(backend.watches))
		}
		if len(backend.pending) != 0 {
			t.Errorf("pending map contains %d operations after Close", len(backend.pending))
		}
		if len(backend.roots) != 0 {
			t.Errorf("recursive root monitor map contains %d entries after Close", len(backend.roots))
		}
		if backend.port != windows.InvalidHandle {
			t.Errorf("completion port is still valid after Close: %v", backend.port)
		}
		for _, watch := range tracked {
			if watch.active != nil {
				t.Error("watch retains an active operation after Close")
			}
			if watch.ino.handle != windows.InvalidHandle {
				t.Errorf("watch retains a valid handle after Close: %v", watch.ino.handle)
			}
		}
	}
	trackedWatches := func(backend *readDirChangesW) []*watch {
		backend.mu.Lock()
		defer backend.mu.Unlock()

		var tracked []*watch
		for _, index := range backend.watches {
			for _, watch := range index {
				tracked = append(tracked, watch)
			}
		}
		return tracked
	}

	t.Run("drains pending read", func(t *testing.T) {
		tmp := t.TempDir()
		w := newWatcher(t, tmp)
		backend := w.b.(*readDirChangesW)
		tracked := trackedWatches(backend)
		if len(tracked) != 1 || tracked[0].active == nil {
			t.Fatalf("expected one watch with pending I/O; got %d watches", len(tracked))
		}

		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		assertClosed(t, backend, tracked)
	})

	t.Run("rearms before close", func(t *testing.T) {
		tmp := t.TempDir()
		file := join(tmp, "file")
		touch(t, file)

		w := newWatcher(t, tmp)
		backend := w.b.(*readDirChangesW)
		for range 100 {
			if err := w.Add(file); err != nil {
				t.Fatal(err)
			}
			if err := w.Remove(file); err != nil {
				t.Fatal(err)
			}
		}
		tracked := trackedWatches(backend)

		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		assertClosed(t, backend, tracked)
	})

	t.Run("close while directory changes arrive", func(t *testing.T) {
		const (
			iterations = 32
			changes    = 32
		)

		for iteration := range iterations {
			dir := t.TempDir()
			w := newWatcher(t, join(dir, "..."))
			backend := w.b.(*readDirChangesW)
			tracked := trackedWatches(backend)

			firstChange := make(chan struct{})
			writerDone := make(chan error, 1)
			go func() {
				for change := range changes {
					name := join(dir, fmt.Sprintf("file-%d-%d", iteration, change))
					if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
						if change == 0 {
							close(firstChange)
						}
						writerDone <- err
						return
					}
					if change == 0 {
						close(firstChange)
					}
					if err := os.Remove(name); err != nil {
						writerDone <- err
						return
					}
				}
				writerDone <- nil
			}()

			<-firstChange
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if err := <-writerDone; err != nil {
				t.Fatal(err)
			}
			assertClosed(t, backend, tracked)
		}
	})

	t.Run("concurrent operations and close", func(t *testing.T) {
		tmp := t.TempDir()
		dirs := make([]string, 8)
		for i := range dirs {
			dirs[i] = join(tmp, fmt.Sprintf("dir-%d", i))
			mkdir(t, dirs[i])
		}

		w := newWatcher(t, dirs...)
		backend := w.b.(*readDirChangesW)
		tracked := trackedWatches(backend)

		start := make(chan struct{})
		errs := make(chan error, len(dirs)*2+1)
		var wg sync.WaitGroup
		for _, dir := range dirs {
			dir := dir
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				if err := w.Add(dir); err != nil && !errors.Is(err, ErrClosed) {
					errs <- fmt.Errorf("Add(%q): %w", dir, err)
				}
			}()
			go func() {
				defer wg.Done()
				<-start
				if err := w.Remove(dir); err != nil {
					errs <- fmt.Errorf("Remove(%q): %w", dir, err)
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := w.Close(); err != nil {
				errs <- fmt.Errorf("Close: %w", err)
			}
		}()

		close(start)
		wait := make(chan struct{})
		go func() {
			wg.Wait()
			close(wait)
		}()
		select {
		case <-wait:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent Add, Remove, and Close calls did not finish")
		}
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		assertClosed(t, backend, tracked)
	})

	t.Run("concurrent watchers close", func(t *testing.T) {
		const watcherCount = 32

		type watcherState struct {
			w             *Watcher
			backend       *readDirChangesW
			tracked       []*watch
			collectorDone <-chan struct{}
		}
		states := make([]watcherState, watcherCount)
		tmp := t.TempDir()
		for i := range states {
			dir := join(tmp, fmt.Sprintf("dir-%d", i))
			mkdir(t, dir)
			collector := newCollector(t, join(dir, "..."))
			collector.collect(t)
			states[i].w = collector.w
			states[i].backend = states[i].w.b.(*readDirChangesW)
			states[i].tracked = trackedWatches(states[i].backend)
			states[i].collectorDone = collector.done
		}

		type closeResult struct {
			index int
			err   error
		}
		start := make(chan struct{})
		results := make(chan closeResult, watcherCount)
		observed := make(chan int, watcherCount)
		var ready sync.WaitGroup
		ready.Add(watcherCount)
		for i := range states {
			go func(index int, watcher *Watcher) {
				ready.Done()
				<-start
				results <- closeResult{index, watcher.Close()}
			}(i, states[i].w)
			go func(index int, done <-chan struct{}) {
				<-done
				observed <- index
			}(i, states[i].collectorDone)
		}

		ready.Wait()
		close(start)
		timer := time.NewTimer(time.Second)
		defer timer.Stop()

		closeCompleted := make([]bool, watcherCount)
		closeCount := 0
		streamObserved := make([]bool, watcherCount)
		streamCount := 0
		for closeCount < watcherCount || streamCount < watcherCount {
			select {
			case result := <-results:
				closeCompleted[result.index] = true
				closeCount++
				if result.err != nil {
					t.Errorf("watcher %d: %v", result.index, result.err)
				}
			case index := <-observed:
				streamObserved[index] = true
				streamCount++
			case <-timer.C:
				t.Errorf(
					"within 1s: %d of %d watchers did not close; %d of %d collectors did not observe closed streams",
					watcherCount-closeCount,
					watcherCount,
					watcherCount-streamCount,
					watcherCount,
				)
				for i, state := range states {
					if closeCompleted[i] && streamObserved[i] {
						continue
					}
					state.backend.mu.Lock()
					port := state.backend.port
					watches := len(state.backend.watches)
					state.backend.mu.Unlock()
					t.Logf(
						"watcher %d: close=%t stream=%t port=%v watch volumes=%d",
						i,
						closeCompleted[i],
						streamObserved[i],
						port,
						watches,
					)
				}

				cleanup := time.NewTimer(5 * time.Second)
				for closeCount < watcherCount || streamCount < watcherCount {
					select {
					case result := <-results:
						closeCompleted[result.index] = true
						closeCount++
						if result.err != nil {
							t.Errorf("watcher %d: %v", result.index, result.err)
						}
					case index := <-observed:
						streamObserved[index] = true
						streamCount++
					case <-cleanup.C:
						t.Fatal("timed out cleaning up concurrently closing watchers")
					}
				}
				cleanup.Stop()
			}
		}

		for i, state := range states {
			t.Run(fmt.Sprintf("watcher-%d", i), func(t *testing.T) {
				assertClosed(t, state.backend, state.tracked)
			})
		}
	})

	t.Run("concurrent close", func(t *testing.T) {
		tmp := t.TempDir()
		w := newWatcher(t, tmp)
		backend := w.b.(*readDirChangesW)
		tracked := trackedWatches(backend)

		errs := make(chan error, 32)
		var wg sync.WaitGroup
		for range 32 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- w.Close()
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Error(err)
			}
		}
		assertClosed(t, backend, tracked)
	})
}

func TestWindowsRecursiveRootRenameCleanup(t *testing.T) {
	parent := t.TempDir()
	a := join(parent, "a")
	ab := join(parent, "ab")
	renamed := join(parent, "renamed")
	mkdir(t, a)
	mkdir(t, ab)

	w := newWatcher(t)
	defer w.Close()
	addWatch(t, w, join(a, "..."))
	addWatch(t, w, join(ab, "..."))

	if err := os.Rename(a, renamed); err != nil {
		t.Fatal(err)
	}

	waitForWatchList := func(want ...string) {
		t.Helper()
		slices.Sort(want)
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			got := w.WatchList()
			slices.Sort(got)
			if slices.Equal(got, want) {
				return
			}
			select {
			case _, ok := <-w.Events:
				if !ok {
					t.Fatal("Events closed while waiting for recursive root cleanup")
				}
			case err, ok := <-w.Errors:
				if !ok {
					t.Fatal("Errors closed while waiting for recursive root cleanup")
				}
				t.Fatalf("watcher error: %v", err)
			case <-ticker.C:
			case <-timeout.C:
				t.Fatalf("WatchList after root rename = %q; want %q", got, want)
			}
		}
	}
	waitForWatchList(ab)

	waitForPathOp := func(path string, op Op) {
		t.Helper()
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					t.Fatal("Events closed while waiting for recursive root cleanup")
				}
				if event.Name == path && event.Has(op) {
					return
				}
			case err, ok := <-w.Errors:
				if !ok {
					t.Fatal("Errors closed while waiting for recursive root cleanup")
				}
				t.Fatalf("watcher error: %v", err)
			case <-timeout.C:
				t.Fatalf("timed out waiting for %s on %q", op, path)
			}
		}
	}

	backend := w.b.(*readDirChangesW)
	backend.mu.Lock()
	_, staleRoot := backend.roots[a]
	_, siblingRoot := backend.roots[ab]
	backend.mu.Unlock()
	if staleRoot {
		t.Errorf("renamed recursive root retained in monitor state: %q", a)
	}
	if !siblingRoot {
		t.Errorf("prefix-similar recursive root lost from monitor state: %q", ab)
	}

	sentinel := join(ab, "sentinel")
	touch(t, sentinel, noWait)
	waitForPathOp(sentinel, Create)
}

func TestWindowsRecursiveAddRollback(t *testing.T) {
	root := t.TempDir()
	file := join(root, "file")
	touch(t, file)

	watcher := newWatcher(t, root)
	defer watcher.Close()
	before := watcher.WatchList()

	if err := watcher.Add(join(file, "...")); err == nil {
		t.Fatal("recursive Add(file) succeeded")
	}
	if err := watcher.Add(join(root, "missing", "...")); err == nil {
		t.Fatal("recursive Add(missing) succeeded")
	}
	if got := watcher.WatchList(); !slices.Equal(got, before) {
		t.Fatalf("WatchList after failed recursive Add = %q; want %q", got, before)
	}

	backend := watcher.b.(*readDirChangesW)
	backend.mu.Lock()
	defer backend.mu.Unlock()
	watches := 0
	for _, index := range backend.watches {
		for _, watch := range index {
			watches++
			if watch.recurse {
				t.Errorf("recursive watch retained after failed Add: %q", watch.path)
			}
		}
	}
	if watches != 1 {
		t.Fatalf("physical watches after failed recursive Add = %d; want 1", watches)
	}
}
