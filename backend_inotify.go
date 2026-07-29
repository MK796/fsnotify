//go:build linux && !appengine

package fsnotify

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unsafe"

	"github.com/fsnotify/fsnotify/internal"
	"golang.org/x/sys/unix"
)

// inotify does not enqueue IN_MOVED_FROM/IN_MOVED_TO pairs atomically. Keep an
// unmatched directory move briefly before treating it as a move out.
const inotifyRenameTimeout = 10 * time.Millisecond

type inotify struct {
	*shared
	Events chan Event
	Errors chan error

	// Store fd here as os.File.Read() will no longer return on close after
	// calling Fd(). See: https://github.com/golang/go/issues/26439
	fd          int
	inotifyFile *os.File
	watches     *watches
	doneResp    chan struct{} // Channel to respond to Close

	// Store rename cookies in an array, with the index wrapping to 0. Almost
	// all of the time what we get is a MOVED_FROM to set the cookie and the
	// next event inotify sends will be MOVED_TO to read it. However, this is
	// not guaranteed – as described in inotify(7) – and we may get other events
	// between the two MOVED_* events (including other MOVED_* ones).
	//
	// A second issue is that moving a file outside the watched directory will
	// trigger a MOVED_FROM to set the cookie, but we never see the MOVED_TO to
	// read and delete it. So just storing it in a map would slowly leak memory.
	//
	// Doing it like this gives us a simple fast LRU-cache that won't allocate.
	// Ten items should be more than enough for our purpose, and a loop over
	// such a short array is faster than a map access anyway (not that it hugely
	// matters since we're talking about hundreds of ns at the most, but still).
	cookies     [10]koekje
	cookieIndex uint8
	cookiesMu   sync.Mutex

	pendingMoves map[uint32]pendingMove
	readDeadline time.Time
}

type (
	watches struct {
		wd      map[uint32]*watch              // wd → physical watch
		path    map[string]uint32              // physical path → wd
		byUser  map[string]Op                  // paths explicitly added by the user
		target  map[string]string              // user path → current physical path
		recurse map[string]struct{}            // recursive user roots
		owners  map[string]map[string]struct{} // physical path → user roots
	}
	watch struct {
		wd         uint32 // Watch descriptor (as returned by the inotify_add_watch() syscall)
		flags      uint32 // inotify flags of this watch (see inotify(7) for the list of valid flags)
		path       string // Watch path.
		watchFlags watchFlag
	}
	koekje struct {
		cookie uint32
		path   string
	}
	pendingMove struct {
		path string
	}
	watchRegistration struct {
		path   string
		owners []string
	}
)

func (w watch) byUser() bool  { return w.watchFlags&flagByUser != 0 }
func (w watch) recurse() bool { return w.watchFlags&flagRecurse != 0 }

func newWatches() *watches {
	return &watches{
		wd:      make(map[uint32]*watch),
		path:    make(map[string]uint32),
		byUser:  make(map[string]Op),
		target:  make(map[string]string),
		recurse: make(map[string]struct{}),
		owners:  make(map[string]map[string]struct{}),
	}
}

func (w *watches) byPath(path string) *watch { return w.wd[w.path[path]] }
func (w *watches) byWd(wd uint32) *watch     { return w.wd[wd] }
func (w *watches) len() int                  { return len(w.wd) }
func (w *watches) add(ww *watch)             { w.wd[ww.wd] = ww; w.path[ww.path] = ww.wd }
func (w *watches) remove(watch *watch) {
	var removedOwners []string
	for owner := range w.owners[watch.path] {
		if w.target[owner] != watch.path {
			continue
		}
		removedOwners = append(removedOwners, owner)
		delete(w.byUser, owner)
		delete(w.target, owner)
		delete(w.recurse, owner)
	}

	delete(w.path, watch.path)
	delete(w.wd, watch.wd)
	delete(w.owners, watch.path)
	if len(removedOwners) == 0 {
		return
	}

	for path, owners := range w.owners {
		changed := false
		for _, owner := range removedOwners {
			if _, ok := owners[owner]; ok {
				delete(owners, owner)
				changed = true
			}
		}
		if !changed {
			continue
		}
		if len(owners) == 0 {
			delete(w.owners, path)
		}
		w.updateFlags(path)
	}
}

func (w *watches) addUser(path string, recursive bool, op Op) {
	w.byUser[path] = op
	w.target[path] = path
	if recursive {
		w.recurse[path] = struct{}{}
	}
}

func (w *watches) addOwner(path, owner string) {
	owners := w.owners[path]
	if owners == nil {
		owners = make(map[string]struct{}, 1)
		w.owners[path] = owners
	}
	owners[owner] = struct{}{}
	w.updateFlags(path)
}

func (w *watches) recursiveOwners(path string) []string {
	owners := w.owners[path]
	out := make([]string, 0, len(owners))
	for owner := range owners {
		if _, ok := w.recurse[owner]; ok {
			out = append(out, owner)
		}
	}
	return out
}

func (w *watches) opForOwners(owners []string) Op {
	var op Op
	for _, owner := range owners {
		op |= w.byUser[owner]
	}
	return op
}

func (w *watches) opForPath(path string) Op {
	var op Op
	for owner := range w.owners[path] {
		op |= w.byUser[owner]
	}
	return op
}

func (w *watches) updateFlags(path string) {
	wd, ok := w.path[path]
	if !ok {
		return
	}
	watch := w.wd[wd]
	watch.watchFlags = 0
	for owner := range w.owners[path] {
		if w.target[owner] == path {
			watch.watchFlags |= flagByUser
		}
		if _, ok := w.recurse[owner]; ok {
			watch.watchFlags |= flagRecurse
		}
	}
}

func (w *watches) releaseOwner(owner string, requireRecursive bool) ([]uint32, error) {
	if _, ok := w.byUser[owner]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNonExistentWatch, owner)
	}
	if _, recursive := w.recurse[owner]; requireRecursive && !recursive {
		return nil, fmt.Errorf("can't use /... with non-recursive watch %q", owner)
	}

	delete(w.byUser, owner)
	delete(w.target, owner)
	delete(w.recurse, owner)

	type unusedWatch struct {
		path string
		wd   uint32
	}
	var unused []unusedWatch
	for path, owners := range w.owners {
		delete(owners, owner)
		if len(owners) == 0 {
			unused = append(unused, unusedWatch{path: path, wd: w.path[path]})
			continue
		}
		w.updateFlags(path)
	}
	sort.Slice(unused, func(i, j int) bool {
		return len(unused[i].path) > len(unused[j].path)
	})

	wds := make([]uint32, 0, len(unused))
	for _, item := range unused {
		delete(w.owners, item.path)
		delete(w.path, item.path)
		delete(w.wd, item.wd)
		wds = append(wds, item.wd)
	}
	return wds, nil
}

func (w *watches) detachSubtree(root string) []uint32 {
	removedOwners := make(map[string]struct{})
	for owner, target := range w.target {
		if target == root || hasPathPrefix(target, root) {
			removedOwners[owner] = struct{}{}
			delete(w.byUser, owner)
			delete(w.target, owner)
			delete(w.recurse, owner)
		}
	}

	for path, owners := range w.owners {
		changed := false
		for owner := range removedOwners {
			if _, ok := owners[owner]; ok {
				delete(owners, owner)
				changed = true
			}
		}
		if changed && path != root && !hasPathPrefix(path, root) {
			w.updateFlags(path)
		}
	}

	var wds []uint32
	for path, wd := range w.path {
		if path != root && !hasPathPrefix(path, root) {
			continue
		}
		delete(w.path, path)
		delete(w.wd, wd)
		delete(w.owners, path)
		wds = append(wds, wd)
	}
	return wds
}

func (w *watches) rollbackRegistrations(registrations []watchRegistration) []uint32 {
	var unused []uint32
	for i := len(registrations) - 1; i >= 0; i-- {
		registration := registrations[i]
		owners, exists := w.owners[registration.path]
		if !exists {
			continue
		}
		for _, owner := range registration.owners {
			delete(owners, owner)
		}
		if len(owners) != 0 {
			w.updateFlags(registration.path)
			continue
		}

		wd, exists := w.path[registration.path]
		if !exists {
			delete(w.owners, registration.path)
			continue
		}
		delete(w.owners, registration.path)
		delete(w.path, registration.path)
		delete(w.wd, wd)
		unused = append(unused, wd)
	}
	return unused
}

func (w *watches) rebase(oldPath, newPath string, destinationOwners []string) []uint32 {
	var unused []uint32
	for path := range w.path {
		atDestination := path == newPath || hasPathPrefix(path, newPath)
		fromSource := path == oldPath || hasPathPrefix(path, oldPath)
		if atDestination && !fromSource {
			unused = append(unused, w.detachSubtree(newPath)...)
			break
		}
	}

	// Explicit watches are removed on rename. The physical subtree can remain
	// watched only through recursive owners that cover its destination.
	renamedOwners := make(map[string]struct{})
	for owner, target := range w.target {
		if target == oldPath || hasPathPrefix(target, oldPath) {
			renamedOwners[owner] = struct{}{}
			delete(w.byUser, owner)
			delete(w.target, owner)
			delete(w.recurse, owner)
		}
	}
	newOwners := make(map[string]struct{}, len(destinationOwners))
	for _, owner := range destinationOwners {
		if _, recursive := w.recurse[owner]; recursive {
			newOwners[owner] = struct{}{}
		}
	}
	if len(renamedOwners) != 0 {
		for path, owners := range w.owners {
			if path == oldPath || hasPathPrefix(path, oldPath) {
				continue
			}
			for owner := range renamedOwners {
				delete(owners, owner)
			}
			if len(owners) == 0 {
				wd := w.path[path]
				delete(w.path, path)
				delete(w.wd, wd)
				delete(w.owners, path)
				unused = append(unused, wd)
				continue
			}
			w.updateFlags(path)
		}
	}

	type move struct {
		from   string
		to     string
		wd     uint32
		owners map[string]struct{}
	}
	var moved []move
	for path, wd := range w.path {
		if path == oldPath || hasPathPrefix(path, oldPath) {
			owners := make(map[string]struct{})
			for owner := range w.owners[path] {
				_, ownsDestination := newOwners[owner]
				if ownsDestination {
					owners[owner] = struct{}{}
				}
			}
			for owner := range newOwners {
				owners[owner] = struct{}{}
			}
			moved = append(moved, move{
				from:   path,
				to:     newPath + path[len(oldPath):],
				wd:     wd,
				owners: owners,
			})
		}
	}
	for _, move := range moved {
		delete(w.path, move.from)
		delete(w.owners, move.from)
	}
	for _, move := range moved {
		if len(move.owners) == 0 {
			delete(w.wd, move.wd)
			unused = append(unused, move.wd)
			continue
		}
		w.path[move.to] = move.wd
		watch := w.wd[move.wd]
		watch.path = move.to
		w.wd[move.wd] = watch
		w.owners[move.to] = move.owners
	}
	for _, move := range moved {
		if len(move.owners) != 0 {
			w.updateFlags(move.to)
		}
	}
	return unused
}

func (w *watches) updatePath(path string, f func(*watch) (*watch, error)) error {
	var existing *watch
	wd, ok := w.path[path]
	if ok {
		existing = w.wd[wd]
	}

	upd, err := f(existing)
	if err != nil {
		return err
	}
	if upd != nil {
		w.wd[upd.wd] = upd
		w.path[upd.path] = upd.wd

		if upd.wd != wd {
			delete(w.wd, wd)
		}
	}

	return nil
}

var defaultBufferSize = 0

func newBackend(ev chan Event, errs chan error) (backend, error) {
	// Need to set nonblocking mode for SetDeadline to work, otherwise blocking
	// I/O operations won't terminate on close.
	fd, errno := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if fd == -1 {
		return nil, fmt.Errorf("fsnotify: initializing inotify: %w", errno)
	}

	w := &inotify{
		shared:       newShared(ev, errs),
		Events:       ev,
		Errors:       errs,
		fd:           fd,
		inotifyFile:  os.NewFile(uintptr(fd), ""),
		watches:      newWatches(),
		doneResp:     make(chan struct{}),
		pendingMoves: make(map[uint32]pendingMove),
	}

	go w.readEvents()
	return w, nil
}

func (w *inotify) Close() error {
	if w.shared.close() {
		return nil
	}

	w.mu.Lock()
	// Causes any blocking reads to return with an error, provided the file
	// still supports deadline operations.
	err := w.inotifyFile.Close()
	// Closing the inotify descriptor removes all kernel watches. Calling
	// inotify_rm_watch after this point could operate on an fd that the kernel
	// has already reused for another watcher.
	w.watches = newWatches()
	w.mu.Unlock()
	if err != nil {
		return err
	}

	<-w.doneResp // Wait for readEvents() to finish.
	return nil
}

func (w *inotify) Add(name string) error { return w.AddWith(name) }

func inotifyFlags(op Op, recursive bool) uint32 {
	var flags uint32
	if op.Has(Create) {
		flags |= unix.IN_CREATE
	}
	if op.Has(Write) {
		flags |= unix.IN_MODIFY
	}
	if op.Has(Remove) {
		flags |= unix.IN_DELETE | unix.IN_DELETE_SELF
	}
	if op.Has(Rename) {
		flags |= unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_MOVE_SELF
	}
	if op.Has(Chmod) {
		flags |= unix.IN_ATTRIB
	}
	if op.Has(xUnportableOpen) {
		flags |= unix.IN_OPEN
	}
	if op.Has(xUnportableRead) {
		flags |= unix.IN_ACCESS
	}
	if op.Has(xUnportableCloseWrite) {
		flags |= unix.IN_CLOSE_WRITE
	}
	if op.Has(xUnportableCloseRead) {
		flags |= unix.IN_CLOSE_NOWRITE
	}
	if recursive {
		flags |= unix.IN_CREATE | unix.IN_DELETE | unix.IN_DELETE_SELF |
			unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_MOVE_SELF
	}
	return flags
}

func (w *inotify) AddWith(path string, opts ...addOpt) error {
	if w.isClosed() {
		return ErrClosed
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  AddWith(%q)\n",
			time.Now().Format("15:04:05.000000000"), path)
	}

	with := getOptions(opts...)
	if !w.xSupports(with.op) {
		return fmt.Errorf("%w: %s", xErrUnsupported, with.op)
	}
	path, recurse := recursivePath(path)
	flags := inotifyFlags(with.op, recurse)

	add := func(path string, owners ...string) error {
		return w.register(path, flags, owners)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed() {
		return ErrClosed
	}
	if _, exists := w.watches.byUser[path]; exists {
		return nil
	}
	w.watches.addUser(path, recurse, with.op)
	rollback := func() {
		wds, err := w.watches.releaseOwner(path, false)
		if err != nil {
			return
		}
		for _, wd := range wds {
			_, _ = unix.InotifyRmWatch(w.fd, wd)
		}
	}
	if recurse {
		err := filepath.WalkDir(path, func(root string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				if root == path {
					return fmt.Errorf("fsnotify: not a directory: %q", path)
				}
				return nil
			}

			// Send a Create event when adding new directory from a recursive
			// watch; this is for "mkdir -p one/two/three". Usually all those
			// directories will be created before we can set up watchers on the
			// subdirectories, so only "one" would be sent as a Create event and
			// not "one/two" and "one/two/three" (inotifywait -r has the same
			// problem).
			if with.sendCreate && with.op.Has(Create) && root != path {
				w.sendEvent(Event{Name: root, Op: Create})
			}

			return add(root, path)
		})
		if err != nil {
			rollback()
		}
		return err
	}

	err := add(path, path)
	if err != nil {
		rollback()
		return err
	}
	if _, ok := w.watches.path[path]; !ok {
		rollback()
	}
	return nil
}

func (w *inotify) register(path string, flags uint32, owners []string) error {
	err := w.watches.updatePath(path, func(existing *watch) (*watch, error) {
		if existing != nil {
			flags |= existing.flags | unix.IN_MASK_ADD
		}

		wd, err := unix.InotifyAddWatch(w.fd, path, flags)
		if wd == -1 {
			return nil, err
		}

		if e, ok := w.watches.wd[uint32(wd)]; ok {
			return e, nil
		}

		if existing == nil {
			return &watch{
				wd:    uint32(wd),
				path:  path,
				flags: flags,
			}, nil
		}

		existing.wd = uint32(wd)
		existing.flags = flags
		return existing, nil
	})
	if err != nil {
		return err
	}
	// inotify identifies watches by inode. Adding a symlink to an already
	// watched target therefore returns the existing descriptor without adding
	// the symlink path to our path map. Keep this a no-op, as it was before
	// recursive ownership tracking was introduced.
	if _, ok := w.watches.path[path]; !ok {
		return nil
	}
	for _, owner := range owners {
		w.watches.addOwner(path, owner)
	}
	return nil
}

func (w *inotify) registerRecursiveSubtree(root string, owners []string, sendCreate bool) ([]Event, error) {
	if len(owners) == 0 {
		return nil, nil
	}

	var (
		events        []Event
		registrations []watchRegistration
	)
	op := w.watches.opForOwners(owners)
	flags := inotifyFlags(op, true)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && sendCreate {
			events = append(events, Event{Name: path, Op: Create})
		}

		var addedOwners []string
		existingOwners := w.watches.owners[path]
		for _, owner := range owners {
			if _, exists := existingOwners[owner]; !exists {
				addedOwners = append(addedOwners, owner)
			}
		}
		if err := w.register(path, flags, owners); err != nil {
			return err
		}
		if len(addedOwners) != 0 && w.watches.byPath(path) != nil {
			registrations = append(registrations, watchRegistration{
				path:   path,
				owners: addedOwners,
			})
		}
		return nil
	})
	if err == nil {
		return events, nil
	}
	rollbackErr := w.removeWatchDescriptors(w.watches.rollbackRegistrations(registrations))
	return nil, errors.Join(err, rollbackErr)
}

func (w *inotify) Remove(name string) error {
	if w.isClosed() {
		return nil
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  Remove(%q)\n",
			time.Now().Format("15:04:05.000000000"), name)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed() {
		return nil
	}
	return w.remove(filepath.Clean(name))
}

func (w *inotify) remove(name string) error {
	name, recursive := recursivePath(name)
	wds, err := w.watches.releaseOwner(name, recursive)
	if err != nil {
		return err
	}
	return w.removeWatchDescriptors(wds)
}

func (w *inotify) removeWatchDescriptors(wds []uint32) error {
	var errs []error
	for _, wd := range wds {
		_, err := unix.InotifyRmWatch(w.fd, wd)
		// The kernel can invalidate a watch before its IN_IGNORED event has
		// reached readEvents. Our logical watch was still present and has now
		// been removed, so an invalid descriptor needs no further cleanup.
		if errors.Is(err, unix.EINVAL) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w *inotify) detachPendingMoveAt(path string) error {
	found := false
	for cookie, move := range w.pendingMoves {
		if move.path != path {
			continue
		}
		delete(w.pendingMoves, cookie)
		found = true
	}
	if !found {
		return nil
	}
	return w.removeWatchDescriptors(w.watches.detachSubtree(path))
}

func (w *inotify) WatchList() []string {
	if w.isClosed() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed() {
		return nil
	}
	entries := make([]string, 0, len(w.watches.byUser))
	for pathname := range w.watches.byUser {
		entries = append(entries, pathname)
	}
	return entries
}

// readEvents reads from the inotify file descriptor, converts the
// received events into Event objects and sends them via the Events channel
func (w *inotify) readEvents() {
	defer func() {
		close(w.Errors)
		close(w.Events)
		close(w.doneResp)
	}()

	var buf [unix.SizeofInotifyEvent * 4096]byte // Buffer for a maximum of 4096 raw events
	for {
		ok, err := w.prepareRead()
		if err != nil && !w.sendError(err) {
			return
		}
		if !ok {
			return
		}

		n, err := w.inotifyFile.Read(buf[:])
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return
			}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				if err := w.expirePendingMoves(); err != nil && !w.sendError(err) {
					return
				}
				continue
			}
			if !w.sendError(err) {
				return
			}
			continue
		}

		if n < unix.SizeofInotifyEvent {
			err := errors.New("notify: short read in readEvents()") // Read was too short.
			if n == 0 {
				err = io.EOF // If EOF is received. This should really never happen.
			}
			if !w.sendError(err) {
				return
			}
			continue
		}

		// We don't know how many events we just read into the buffer While the
		// offset points to at least one whole event.
		var offset uint32
		for offset <= uint32(n-unix.SizeofInotifyEvent) {
			// Point to the event in the buffer.
			inEvent := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset]))

			if inEvent.Mask&unix.IN_Q_OVERFLOW != 0 {
				if !w.sendError(ErrEventOverflow) {
					return
				}
			}

			ev, extra, err := w.handleEvent(inEvent, &buf, offset)
			if !w.sendError(err) {
				return
			}
			if !w.sendEvent(ev) {
				return
			}
			for _, ev := range extra {
				if !w.sendEvent(ev) {
					return
				}
			}

			// Move to the next event in the buffer
			offset += unix.SizeofInotifyEvent + inEvent.Len
		}
	}
}

func (w *inotify) prepareRead() (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed() {
		return false, nil
	}

	// IN_MOVED_FROM can be the final event in one read while the matching
	// IN_MOVED_TO is waiting in the next. Start the timeout immediately before
	// that next read; time spent processing or delivering the current buffer
	// must not expire the pair before the kernel queue is read again.
	next := time.Time{}
	if len(w.pendingMoves) != 0 {
		next = time.Now().Add(inotifyRenameTimeout)
	}

	if !next.Equal(w.readDeadline) {
		if err := w.inotifyFile.SetReadDeadline(next); err != nil {
			return true, err
		}
		w.readDeadline = next
	}
	return true, nil
}

func (w *inotify) expirePendingMoves() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed() {
		return nil
	}

	var errs []error
	for cookie, move := range w.pendingMoves {
		delete(w.pendingMoves, cookie)
		if err := w.removeWatchDescriptors(w.watches.detachSubtree(move.path)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w *inotify) handleEvent(inEvent *unix.InotifyEvent, buf *[65536]byte, offset uint32) (Event, []Event, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	/// If the event happened to the watched directory or the watched file, the
	/// kernel doesn't append the filename to the event, but we would like to
	/// always fill the the "Name" field with a valid filename. We retrieve the
	/// path of the watch from the "paths" map.
	///
	/// Can be nil if Remove() was called in another goroutine for this path
	/// inbetween reading the events from the kernel and reading the internal
	/// state. Not much we can do about it, so just skip. See #616.
	watch := w.watches.byWd(uint32(inEvent.Wd))
	if watch == nil {
		return Event{}, nil, nil
	}
	op := w.watches.opForPath(watch.path)

	var (
		name    = watch.path
		nameLen = uint32(inEvent.Len)
	)
	if nameLen > 0 {
		name = inotifyEventPath(name, inotifyEventName(buf, offset, nameLen))
	}

	if debug {
		internal.Debug(name, inEvent.Mask, inEvent.Cookie)
	}

	if inEvent.Mask&unix.IN_IGNORED != 0 || inEvent.Mask&unix.IN_UNMOUNT != 0 {
		w.watches.remove(watch)
		return Event{}, nil, nil
	}

	// inotify will automatically remove the watch on deletes; just need
	// to clean our state here.
	if inEvent.Mask&unix.IN_DELETE_SELF == unix.IN_DELETE_SELF {
		w.watches.remove(watch)
	}

	// We can't really update the state when a watched path is moved; only
	// IN_MOVE_SELF is sent and not IN_MOVED_{FROM,TO}. So remove the watch.
	if inEvent.Mask&unix.IN_MOVE_SELF == unix.IN_MOVE_SELF {
		// Watch is set up as part of recurse: do nothing as the move gets
		// registered from the parent directory.
		if watch.recurse() && !watch.byUser() {
			return Event{}, nil, nil
		}

		err := w.remove(watch.path)
		if err != nil && !errors.Is(err, ErrNonExistentWatch) {
			return Event{}, nil, err
		}

		if watch.recurse() {
			ev := Event{Name: watch.path, Op: Rename}
			return filterInotifyEvent(ev, inEvent.Mask, op), nil, nil
		}
	}

	/// Skip if we're watching both this path and the parent; the parent will
	/// already send a delete so no need to do it twice.
	if inEvent.Mask&unix.IN_DELETE_SELF != 0 {
		_, ok := w.watches.path[filepath.Dir(watch.path)]
		if ok {
			return Event{}, nil, nil
		}
	}

	ev := w.newEvent(name, inEvent.Mask, inEvent.Cookie)
	// Need to update watch path for recurse.
	if watch.recurse() {
		isDir := inEvent.Mask&unix.IN_ISDIR == unix.IN_ISDIR
		if isDir && inEvent.Mask&unix.IN_MOVED_FROM != 0 && inEvent.Cookie != 0 {
			if w.watches.byPath(ev.Name) != nil {
				w.pendingMoves[inEvent.Cookie] = pendingMove{
					path: ev.Name,
				}
			}
		}
		/// New directory created: set up watch on it.
		if isDir && ev.Has(Create) {
			// Directory rename, so we need to update all the children.
			//
			// TODO: this is of course pretty slow; we should use a better data
			// structure for storing all of this, e.g. store children in the
			// watch. I have some code for this in my kqueue refactor we can use
			// in the future. Correctness comes first; optimize after profiling.
			if move, ok := w.pendingMoves[inEvent.Cookie]; ok {
				delete(w.pendingMoves, inEvent.Cookie)
				ev.renamedFrom = move.path
				owners := w.watches.recursiveOwners(watch.path)
				if err := w.removeWatchDescriptors(w.watches.rebase(move.path, ev.Name, owners)); err != nil {
					return Event{}, nil, err
				}
			} else {
				// A directory moved out may be replaced at the same path before
				// its unmatched MOVED_FROM expires. The old watch follows the
				// moved inode, so detach it before registering the replacement.
				if err := w.detachPendingMoveAt(ev.Name); err != nil {
					return Event{}, nil, err
				}
				if w.watches.byPath(ev.Name) != nil {
					return Event{}, nil, nil
				}
				owners := w.watches.recursiveOwners(watch.path)
				filtered := filterInotifyEvent(ev, inEvent.Mask, op)
				extra, err := w.registerRecursiveSubtree(ev.Name, owners, filtered.Has(Create))
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return Event{}, nil, err
				}
				return filtered, extra, nil
			}
		}
	}

	return filterInotifyEvent(ev, inEvent.Mask, op), nil, nil
}

func inotifyEventPath(root, entry string) string {
	return filepath.Join(root, entry)
}

func filterInotifyEvent(event Event, mask uint32, op Op) Event {
	event.Op = 0
	if mask&unix.IN_CREATE != 0 && op.Has(Create) {
		event.Op |= Create
	}
	if mask&unix.IN_MOVED_TO != 0 && op.Has(Rename) {
		event.Op |= Create
	}
	if mask&(unix.IN_DELETE_SELF|unix.IN_DELETE) != 0 && op.Has(Remove) {
		event.Op |= Remove
	}
	if mask&unix.IN_MODIFY != 0 && op.Has(Write) {
		event.Op |= Write
	}
	if mask&unix.IN_OPEN != 0 && op.Has(xUnportableOpen) {
		event.Op |= xUnportableOpen
	}
	if mask&unix.IN_ACCESS != 0 && op.Has(xUnportableRead) {
		event.Op |= xUnportableRead
	}
	if mask&unix.IN_CLOSE_WRITE != 0 && op.Has(xUnportableCloseWrite) {
		event.Op |= xUnportableCloseWrite
	}
	if mask&unix.IN_CLOSE_NOWRITE != 0 && op.Has(xUnportableCloseRead) {
		event.Op |= xUnportableCloseRead
	}
	if mask&(unix.IN_MOVE_SELF|unix.IN_MOVED_FROM) != 0 && op.Has(Rename) {
		event.Op |= Rename
	}
	if mask&unix.IN_ATTRIB != 0 && op.Has(Chmod) {
		event.Op |= Chmod
	}
	return event
}

func inotifyEventName(buf *[65536]byte, offset, nameLen uint32) string {
	start := int(offset + unix.SizeofInotifyEvent)
	bytes := (*[unix.PathMax]byte)(unsafe.Pointer(&buf[start]))[:nameLen:nameLen]
	for nameLen > 0 && bytes[nameLen-1] == 0 {
		nameLen--
	}
	return string(bytes[:nameLen])
}

func (w *inotify) newEvent(name string, mask, cookie uint32) Event {
	e := Event{Name: name}
	if mask&unix.IN_CREATE == unix.IN_CREATE || mask&unix.IN_MOVED_TO == unix.IN_MOVED_TO {
		e.Op |= Create
	}
	if mask&unix.IN_DELETE_SELF == unix.IN_DELETE_SELF || mask&unix.IN_DELETE == unix.IN_DELETE {
		e.Op |= Remove
	}
	if mask&unix.IN_MODIFY == unix.IN_MODIFY {
		e.Op |= Write
	}
	if mask&unix.IN_OPEN == unix.IN_OPEN {
		e.Op |= xUnportableOpen
	}
	if mask&unix.IN_ACCESS == unix.IN_ACCESS {
		e.Op |= xUnportableRead
	}
	if mask&unix.IN_CLOSE_WRITE == unix.IN_CLOSE_WRITE {
		e.Op |= xUnportableCloseWrite
	}
	if mask&unix.IN_CLOSE_NOWRITE == unix.IN_CLOSE_NOWRITE {
		e.Op |= xUnportableCloseRead
	}
	if mask&unix.IN_MOVE_SELF == unix.IN_MOVE_SELF || mask&unix.IN_MOVED_FROM == unix.IN_MOVED_FROM {
		e.Op |= Rename
	}
	if mask&unix.IN_ATTRIB == unix.IN_ATTRIB {
		e.Op |= Chmod
	}

	if cookie != 0 {
		if mask&unix.IN_MOVED_FROM == unix.IN_MOVED_FROM {
			w.cookiesMu.Lock()
			w.cookies[w.cookieIndex] = koekje{cookie: cookie, path: e.Name}
			w.cookieIndex++
			if w.cookieIndex > 9 {
				w.cookieIndex = 0
			}
			w.cookiesMu.Unlock()
		} else if mask&unix.IN_MOVED_TO == unix.IN_MOVED_TO {
			w.cookiesMu.Lock()
			var prev string
			for _, c := range w.cookies {
				if c.cookie == cookie {
					prev = c.path
					break
				}
			}
			w.cookiesMu.Unlock()
			e.renamedFrom = prev
		}
	}
	return e
}

func (w *inotify) xSupports(op Op) bool {
	return true // Supports everything.
}

func (w *inotify) state() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for wd, ww := range w.watches.wd {
		fmt.Fprintf(os.Stderr, "%4d: %q  watchFlags=0x%x\n", wd, ww.path, ww.watchFlags)
	}
}
