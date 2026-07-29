//go:build linux

// Note: do not add a test here unless the behaviour is truly specific to this
// backend. fsnotify is a cross-platform library: most tests should be as a
// "script" in testdata/ or in fsnotify_test.go. See CONTRIBUTING.md.

package fsnotify

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func drainInotifyWatcher(t *testing.T, watcher *Watcher) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case _, ok := <-watcher.Events:
				if !ok {
					return
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				t.Errorf("unexpected watcher error: %v", err)
			}
		}
	}()
	t.Cleanup(func() {
		if err := watcher.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
}

func TestInotifyEventPath(t *testing.T) {
	tests := []struct {
		name  string
		root  string
		entry string
		want  string
	}{
		{name: "absolute", root: "/tmp/root", entry: "child", want: "/tmp/root/child"},
		{name: "filesystem root", root: "/", entry: "child", want: "/child"},
		{name: "current directory", root: ".", entry: "child", want: "child"},
		{name: "relative", root: "root", entry: "child", want: "root/child"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inotifyEventPath(tt.root, tt.entry); got != tt.want {
				t.Fatalf("event path for root %q and entry %q: %q; want %q",
					tt.root, tt.entry, got, tt.want)
			}
		})
	}
}

func TestFilterInotifyEvent(t *testing.T) {
	tests := []struct {
		name string
		mask uint32
		op   Op
		want Op
	}{
		{name: "create", mask: unix.IN_CREATE, op: Create, want: Create},
		{name: "structural create", mask: unix.IN_CREATE, op: Write},
		{name: "move from", mask: unix.IN_MOVED_FROM, op: Rename, want: Rename},
		{name: "move to", mask: unix.IN_MOVED_TO, op: Rename, want: Create},
		{name: "structural move to", mask: unix.IN_MOVED_TO, op: Write},
		{name: "delete", mask: unix.IN_DELETE, op: Remove, want: Remove},
		{name: "delete self", mask: unix.IN_DELETE_SELF, op: Remove, want: Remove},
		{name: "write", mask: unix.IN_MODIFY, op: Write, want: Write},
		{name: "chmod", mask: unix.IN_ATTRIB, op: Chmod, want: Chmod},
		{name: "open", mask: unix.IN_OPEN, op: xUnportableOpen, want: xUnportableOpen},
		{name: "read", mask: unix.IN_ACCESS, op: xUnportableRead, want: xUnportableRead},
		{name: "close write", mask: unix.IN_CLOSE_WRITE, op: xUnportableCloseWrite, want: xUnportableCloseWrite},
		{name: "close read", mask: unix.IN_CLOSE_NOWRITE, op: xUnportableCloseRead, want: xUnportableCloseRead},
		{
			name: "combined",
			mask: unix.IN_MOVED_FROM | unix.IN_ATTRIB,
			op:   Rename | Chmod,
			want: Rename | Chmod,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := Event{Name: "/new", Op: Create | Write, renamedFrom: "/old"}
			got := filterInotifyEvent(event, tt.mask, tt.op)
			if got.Op != tt.want {
				t.Fatalf("filtered operation: %s; want %s", got.Op, tt.want)
			}
			if got.Name != event.Name {
				t.Fatalf("filtered name: %q; want %q", got.Name, event.Name)
			}
			if got.renamedFrom != event.renamedFrom {
				t.Fatalf("filtered rename source: %q; want %q", got.renamedFrom, event.renamedFrom)
			}
		})
	}
}

func TestInotifyPrepareReadDoesNotExpirePendingMoves(t *testing.T) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	inotifyFile := os.NewFile(uintptr(fd), "")
	t.Cleanup(func() {
		if err := inotifyFile.Close(); err != nil {
			t.Error(err)
		}
	})
	expired := time.Unix(1, 0)
	if err := inotifyFile.SetReadDeadline(expired); err != nil {
		t.Fatal(err)
	}

	w := &inotify{
		shared:       newShared(make(chan Event), make(chan error)),
		fd:           fd,
		inotifyFile:  inotifyFile,
		watches:      newWatches(),
		pendingMoves: map[uint32]pendingMove{1: {path: "/pending"}},
		readDeadline: expired,
	}
	before := time.Now()

	ok, err := w.prepareRead()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("prepareRead reported a closed watcher")
	}
	if len(w.pendingMoves) != 1 {
		t.Fatalf("pending moves after prepareRead: %d; want 1", len(w.pendingMoves))
	}
	if !w.readDeadline.After(before) {
		t.Fatalf("read deadline after prepareRead: %s; want after %s", w.readDeadline, before)
	}
}

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

	check := func(want int) {
		t.Helper()
		if w.b.(*inotify).watches.len() != want {
			t.Error(w.b.(*inotify).watches)
		}
	}

	check(2)

	// Shouldn't change internal state.
	if err := w.Add("/path-doesnt-exist"); err == nil {
		t.Fatal(err)
	}
	check(2)

	if err := w.Remove(file); err != nil {
		t.Fatal(err)
	}
	check(1)

	if err := w.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	check(0)

	// Make sure Close() cleans up everything.
	addWatch(t, w, tmp)
	addWatch(t, w, file)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	check(0)
}

func TestInotifySymlinkAliasOwnership(t *testing.T) {
	tmp := t.TempDir()
	target := join(tmp, "target")
	link := join(tmp, "link")
	touch(t, target)
	symlink(t, target, link)

	w := newWatcher(t)
	defer w.Close()
	addWatch(t, w, target)
	addWatch(t, w, link)

	have := w.WatchList()
	if len(have) != 1 || have[0] != target {
		t.Fatalf("WatchList after adding symlink alias: %q; want [%q]", have, target)
	}

	if err := w.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := w.Remove(link); !errors.Is(err, ErrNonExistentWatch) {
		t.Fatalf("Remove symlink alias: %T %v; want ErrNonExistentWatch", err, err)
	}
	if have := w.WatchList(); len(have) != 0 {
		t.Fatalf("WatchList after removal: %q; want empty", have)
	}
}

func TestInotifyRecursiveMoveOutDropsInternalWatches(t *testing.T) {
	tmp := t.TempDir()
	root := join(tmp, "root")
	child := join(root, "child")
	mkdir(t, root)
	mkdir(t, child)
	mkdir(t, child, "nested")

	watcher := newWatcher(t, join(root, "..."))
	inotify := watcher.b.(*inotify)
	drainInotifyWatcher(t, watcher)

	if err := os.Rename(child, join(tmp, "moved")); err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		inotify.mu.Lock()
		stale := false
		for path := range inotify.watches.path {
			if path == child || hasPathPrefix(path, child) {
				stale = true
				break
			}
		}
		inotify.mu.Unlock()
		if !stale {
			return
		}

		select {
		case <-deadline.C:
			t.Fatalf("stale watches retained after moving %q out of recursive root", child)
		case <-ticker.C:
		}
	}
}

func TestInotifyRecursiveMoveOutReplacementIsWatched(t *testing.T) {
	tmp := t.TempDir()
	root := join(tmp, "root")
	child := join(root, "child")
	nested := join(child, "nested")
	mkdir(t, root)
	mkdir(t, child)

	watcher := newWatcher(t, join(root, "..."))
	inotify := watcher.b.(*inotify)
	drainInotifyWatcher(t, watcher)

	if err := os.Rename(child, join(tmp, "moved")); err != nil {
		t.Fatal(err)
	}
	mkdir(t, child)
	mkdir(t, nested)

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	var (
		childExists  bool
		nestedExists bool
		pending      int
	)
	for {
		inotify.mu.Lock()
		_, childExists = inotify.watches.path[child]
		_, nestedExists = inotify.watches.path[nested]
		pending = len(inotify.pendingMoves)
		inotify.mu.Unlock()
		if pending == 0 && childExists && nestedExists {
			return
		}

		select {
		case <-deadline.C:
			t.Fatalf("replacement subtree not watched after move-out: pending=%d child=%t nested=%t",
				pending, childExists, nestedExists)
		case <-ticker.C:
		}
	}
}

func TestInotifyRecursiveMoveOutReplacementExhaustion(t *testing.T) {
	const trees = 128

	tmp := t.TempDir()
	root := join(tmp, "root")
	outside := join(tmp, "outside")
	mkdir(t, root, noWait)
	mkdir(t, outside, noWait)
	for i := range trees {
		child := join(root, strconv.Itoa(i))
		mkdir(t, child, noWait)
		mkdir(t, child, "nested", noWait)
	}

	watcher := newWatcher(t, join(root, "..."))
	inotify := watcher.b.(*inotify)
	drainInotifyWatcher(t, watcher)

	for i := range trees {
		name := strconv.Itoa(i)
		child := join(root, name)
		if err := os.Rename(child, join(outside, name)); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			mkdir(t, child, noWait)
			mkdir(t, child, "nested", noWait)
			mkdir(t, child, "replacement", noWait)
		}
	}

	expectedWatches := 1 + (trees/2)*3
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	var (
		pending      int
		paths        int
		descriptors  int
		ownerEntries int
		mismatch     string
	)
	for {
		inotify.mu.Lock()
		pending = len(inotify.pendingMoves)
		paths = len(inotify.watches.path)
		descriptors = len(inotify.watches.wd)
		ownerEntries = len(inotify.watches.owners)
		mismatch = ""
		if pending == 0 && paths == expectedWatches &&
			descriptors == expectedWatches && ownerEntries == expectedWatches {
			for i := range trees {
				child := join(root, strconv.Itoa(i))
				_, childExists := inotify.watches.path[child]
				_, nestedExists := inotify.watches.path[join(child, "nested")]
				_, replacementExists := inotify.watches.path[join(child, "replacement")]
				if i%2 == 0 {
					validOwners := true
					for _, path := range []string{child, join(child, "nested"), join(child, "replacement")} {
						owners := inotify.watches.owners[path]
						_, rootOwns := owners[root]
						if len(owners) != 1 || !rootOwns {
							validOwners = false
							break
						}
					}
					if !childExists || !nestedExists || !replacementExists || !validOwners {
						mismatch = child
						break
					}
				} else if childExists || nestedExists || replacementExists {
					mismatch = child
					break
				}
			}
		} else {
			mismatch = "watch counts"
		}
		inotify.mu.Unlock()
		if mismatch == "" {
			break
		}

		select {
		case <-deadline.C:
			t.Fatalf("move-out exhaustion did not converge: pending=%d paths=%d descriptors=%d owners=%d expected=%d mismatch=%q",
				pending, paths, descriptors, ownerEntries, expectedWatches, mismatch)
		case <-ticker.C:
		}
	}

	if err := watcher.Remove(root); err != nil {
		t.Fatal(err)
	}
	inotify.mu.Lock()
	defer inotify.mu.Unlock()
	if len(inotify.pendingMoves) != 0 ||
		len(inotify.watches.path) != 0 ||
		len(inotify.watches.wd) != 0 ||
		len(inotify.watches.byUser) != 0 ||
		len(inotify.watches.target) != 0 ||
		len(inotify.watches.recurse) != 0 ||
		len(inotify.watches.owners) != 0 {
		t.Fatalf("watch state retained after exhaustion cleanup: pending=%d paths=%d descriptors=%d users=%d targets=%d roots=%d owners=%d",
			len(inotify.pendingMoves),
			len(inotify.watches.path),
			len(inotify.watches.wd),
			len(inotify.watches.byUser),
			len(inotify.watches.target),
			len(inotify.watches.recurse),
			len(inotify.watches.owners),
		)
	}
}

func TestInotifyRecursiveRenameTransfersOwnership(t *testing.T) {
	tmp := t.TempDir()
	source := join(tmp, "source")
	destination := join(tmp, "destination")
	child := join(source, "child")
	moved := join(destination, "child")
	mkdir(t, source)
	mkdir(t, destination)
	mkdir(t, child)
	mkdir(t, child, "nested")

	watcher := newWatcher(t, join(source, "..."), join(destination, "..."))
	inotify := watcher.b.(*inotify)
	drainInotifyWatcher(t, watcher)

	if err := os.Rename(child, moved); err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		inotify.mu.Lock()
		_, oldExists := inotify.watches.path[child]
		_, newExists := inotify.watches.path[moved]
		_, sourceOwns := inotify.watches.owners[moved][source]
		_, destinationOwns := inotify.watches.owners[moved][destination]
		inotify.mu.Unlock()
		if !oldExists && newExists && !sourceOwns && destinationOwns {
			return
		}

		select {
		case <-deadline.C:
			t.Fatalf("recursive ownership not transferred after moving %q to %q: old=%t new=%t source=%t destination=%t",
				child, moved, oldExists, newExists, sourceOwns, destinationOwns)
		case <-ticker.C:
		}
	}
}

func TestInotifyRecursiveRootRenameRemovesExplicitWatch(t *testing.T) {
	tmp := t.TempDir()
	outer := join(tmp, "outer")
	inner := join(outer, "inner")
	renamed := join(outer, "renamed")
	nested := join(inner, "nested")
	mkdir(t, outer)
	mkdir(t, inner)
	mkdir(t, nested)

	watcher := newWatcher(t, join(outer, "..."), join(inner, "..."))
	inotify := watcher.b.(*inotify)
	drainInotifyWatcher(t, watcher)

	if err := os.Rename(inner, renamed); err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		inotify.mu.Lock()
		_, innerUserWatch := inotify.watches.byUser[inner]
		_, innerTarget := inotify.watches.target[inner]
		_, oldPath := inotify.watches.path[inner]
		_, renamedPath := inotify.watches.path[renamed]
		_, nestedPath := inotify.watches.path[join(renamed, "nested")]
		inotify.mu.Unlock()
		if !innerUserWatch && !innerTarget && !oldPath && renamedPath && nestedPath {
			break
		}

		select {
		case <-deadline.C:
			t.Fatalf("explicit recursive watch retained after rename: user=%t target=%t old=%t renamed=%t nested=%t",
				innerUserWatch, innerTarget, oldPath, renamedPath, nestedPath)
		case <-ticker.C:
		}
	}

	if err := watcher.Remove(inner); !errors.Is(err, ErrNonExistentWatch) {
		t.Fatalf("Remove renamed recursive root: %T %v; want ErrNonExistentWatch", err, err)
	}
}

func TestInotifyRecursiveAncestorRenameRemovesExplicitDescendantWatch(t *testing.T) {
	tmp := t.TempDir()
	outer := join(tmp, "outer")
	ancestor := join(outer, "ancestor")
	inner := join(ancestor, "inner")
	renamed := join(outer, "renamed")
	renamedInner := join(renamed, "inner")
	mkdir(t, outer, noWait)
	mkdir(t, ancestor, noWait)
	mkdir(t, inner, noWait)
	mkdir(t, inner, "nested", noWait)

	watcher := newWatcher(t, join(outer, "..."), join(inner, "..."))
	inotify := watcher.b.(*inotify)
	drainInotifyWatcher(t, watcher)

	if err := os.Rename(ancestor, renamed); err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		inotify.mu.Lock()
		_, innerUserWatch := inotify.watches.byUser[inner]
		_, innerTarget := inotify.watches.target[inner]
		_, oldPath := inotify.watches.path[ancestor]
		_, renamedPath := inotify.watches.path[renamed]
		_, renamedInnerPath := inotify.watches.path[renamedInner]
		_, nestedPath := inotify.watches.path[join(renamedInner, "nested")]
		inotify.mu.Unlock()
		if !innerUserWatch && !innerTarget && !oldPath &&
			renamedPath && renamedInnerPath && nestedPath {
			break
		}

		select {
		case <-deadline.C:
			t.Fatalf("explicit descendant watch retained after ancestor rename: user=%t target=%t old=%t renamed=%t inner=%t nested=%t",
				innerUserWatch, innerTarget, oldPath, renamedPath, renamedInnerPath, nestedPath)
		case <-ticker.C:
		}
	}

	if err := watcher.Remove(inner); !errors.Is(err, ErrNonExistentWatch) {
		t.Fatalf("Remove descendant of renamed directory: %T %v; want ErrNonExistentWatch", err, err)
	}
}

func TestInotifyRecursiveAddRollback(t *testing.T) {
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

	watcher := newWatcher(t)
	defer watcher.Close()
	if err := watcher.Add(join(root, "...")); err == nil {
		t.Fatal("recursive Add succeeded with an unreadable subtree")
	}

	inotify := watcher.b.(*inotify)
	if got := watcher.WatchList(); len(got) != 0 {
		t.Fatalf("WatchList after failed recursive Add: %q", got)
	}
	inotify.mu.Lock()
	defer inotify.mu.Unlock()
	if len(inotify.watches.path) != 0 ||
		len(inotify.watches.wd) != 0 ||
		len(inotify.watches.byUser) != 0 ||
		len(inotify.watches.target) != 0 ||
		len(inotify.watches.recurse) != 0 ||
		len(inotify.watches.owners) != 0 {
		t.Fatalf("watch state retained after failed recursive Add: paths=%d descriptors=%d users=%d targets=%d roots=%d owners=%d",
			len(inotify.watches.path),
			len(inotify.watches.wd),
			len(inotify.watches.byUser),
			len(inotify.watches.target),
			len(inotify.watches.recurse),
			len(inotify.watches.owners),
		)
	}
}

func TestInotifyRecursiveSubtreeRegistrationRollback(t *testing.T) {
	tmp := t.TempDir()
	root := join(tmp, "root")
	incoming := join(tmp, "incoming")
	denied := join(incoming, "z-denied")
	moved := join(root, "incoming")
	mkdir(t, root)
	mkdir(t, incoming)
	mkdir(t, incoming, "a-allowed")
	mkdir(t, denied)
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(denied, 0o755)
		_ = os.Chmod(join(moved, "z-denied"), 0o755)
	})

	watcher := newWatcher(t, join(root, "..."))
	defer watcher.Close()
	if err := os.Rename(incoming, moved); err != nil {
		t.Fatal(err)
	}

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case err, ok := <-watcher.Errors:
			if !ok {
				t.Fatal("Errors channel closed before subtree registration failed")
			}
			if err == nil {
				continue
			}

			inotify := watcher.b.(*inotify)
			inotify.mu.Lock()
			defer inotify.mu.Unlock()
			for path := range inotify.watches.path {
				if path == moved || hasPathPrefix(path, moved) {
					t.Fatalf("partial watch retained after subtree registration failed: %q", path)
				}
			}
			return
		case _, ok := <-watcher.Events:
			if !ok {
				t.Fatal("Events channel closed before subtree registration failed")
			}
		case <-timeout.C:
			t.Fatal("timed out waiting for subtree registration failure")
		}
	}
}

func TestInotifyRecursiveAddExistingPathIsNoop(t *testing.T) {
	root := t.TempDir()
	child := join(root, "child")
	mkdir(t, child)

	watcher := newWatcher(t, root)
	defer watcher.Close()
	if err := watcher.Add(join(root, "...")); err != nil {
		t.Fatal(err)
	}

	inotify := watcher.b.(*inotify)
	inotify.mu.Lock()
	defer inotify.mu.Unlock()
	if _, recursive := inotify.watches.recurse[root]; recursive {
		t.Fatalf("second Add upgraded existing non-recursive watch %q", root)
	}
	if _, watched := inotify.watches.path[child]; watched {
		t.Fatalf("second Add registered recursive child %q", child)
	}
}

func TestInotifyRemoveInvalidatedWatch(t *testing.T) {
	tmp := t.TempDir()
	watcher := newWatcher(t)
	defer watcher.Close()
	addWatch(t, watcher, tmp)

	w := watcher.b.(*inotify)
	w.mu.Lock()
	wd := w.watches.path[tmp]
	if _, err := unix.InotifyRmWatch(w.fd, wd); err != nil {
		w.mu.Unlock()
		t.Fatal(err)
	}
	err := w.remove(tmp)
	w.mu.Unlock()
	if err != nil {
		t.Fatalf("remove after kernel invalidation: %v", err)
	}
}

// Ensure that the correct error is returned on overflows.
func TestInotifyOverflow(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	w := newWatcher(t)
	defer w.Close()

	// We need to generate many more events than the
	// fs.inotify.max_queued_events sysctl setting.
	numDirs, numFiles := 128, 1024

	// All events need to be in the inotify queue before pulling events off it
	// to trigger this error.
	var wg sync.WaitGroup
	for i := range numDirs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			dir := join(tmp, strconv.Itoa(i))
			mkdir(t, dir, noWait)
			addWatch(t, w, dir)

			createFiles(t, dir, "", numFiles, 10*time.Second)
		}(i)
	}
	wg.Wait()

	var (
		creates   = 0
		overflows = 0
	)
	for overflows == 0 && creates < numDirs*numFiles {
		select {
		case <-time.After(10 * time.Second):
			t.Fatalf("Not done")
		case err := <-w.Errors:
			if !errors.Is(err, ErrEventOverflow) {
				t.Fatalf("unexpected error from watcher: %v", err)
			}
			overflows++
		case e := <-w.Events:
			if !strings.HasPrefix(e.Name, tmp) {
				t.Fatalf("Event for unknown file: %s", e.Name)
			}
			if e.Op == Create {
				creates++
			}
		}
	}

	if creates == numDirs*numFiles {
		t.Fatalf("could not trigger overflow")
	}
	if overflows == 0 {
		t.Fatalf("no overflow and not enough CREATE events (expected %d, got %d)",
			numDirs*numFiles, creates)
	}
}

// Test inotify's "we don't send REMOVE until all file descriptors are removed"
// behaviour.
func TestInotifyDeleteOpenFile(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	file := join(tmp, "file")

	touch(t, file)
	fp, err := os.Open(file)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	defer fp.Close()

	w := newCollector(t, file)
	w.collect(t)

	rm(t, file)
	waitForEvents()
	e := w.events(t)
	cmpEvents(t, tmp, e, newEvents(t, `chmod /file`))

	fp.Close()
	e = w.stop(t)
	cmpEvents(t, tmp, e, newEvents(t, `remove /file`))
}
