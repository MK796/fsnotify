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
}

type (
	watches struct {
		wd      map[uint32]*watch              // wd → physical watch
		path    map[string]uint32              // physical path → wd
		byUser  map[string]struct{}            // paths explicitly added by the user
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
)

func (w watch) byUser() bool  { return w.watchFlags&flagByUser != 0 }
func (w watch) recurse() bool { return w.watchFlags&flagRecurse != 0 }

func newWatches() *watches {
	return &watches{
		wd:      make(map[uint32]*watch),
		path:    make(map[string]uint32),
		byUser:  make(map[string]struct{}),
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
	delete(w.path, watch.path)
	delete(w.wd, watch.wd)
	delete(w.owners, watch.path)
}

func (w *watches) addUser(path string, recursive bool) {
	w.byUser[path] = struct{}{}
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

func (w *watches) rebase(oldPath, newPath string) {
	type move struct {
		from string
		to   string
		wd   uint32
	}
	var moved []move
	for path, wd := range w.path {
		if path == oldPath || hasPathPrefix(path, oldPath) {
			moved = append(moved, move{
				from: path,
				to:   newPath + path[len(oldPath):],
				wd:   wd,
			})
		}
	}
	for _, move := range moved {
		delete(w.path, move.from)
	}
	for _, move := range moved {
		w.path[move.to] = move.wd
		watch := w.wd[move.wd]
		watch.path = move.to
		w.wd[move.wd] = watch
		if owners, ok := w.owners[move.from]; ok {
			delete(w.owners, move.from)
			w.owners[move.to] = owners
		}
	}
	for owner, target := range w.target {
		if target == oldPath || hasPathPrefix(target, oldPath) {
			w.target[owner] = newPath + target[len(oldPath):]
		}
	}
	for _, move := range moved {
		w.updateFlags(move.to)
	}
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
		shared:      newShared(ev, errs),
		Events:      ev,
		Errors:      errs,
		fd:          fd,
		inotifyFile: os.NewFile(uintptr(fd), ""),
		watches:     newWatches(),
		doneResp:    make(chan struct{}),
	}

	go w.readEvents()
	return w, nil
}

func (w *inotify) Close() error {
	if w.shared.close() {
		return nil
	}

	// Causes any blocking reads to return with an error, provided the file
	// still supports deadline operations.
	err := w.inotifyFile.Close()
	if err != nil {
		return err
	}
	w.mu.Lock()
	for name := range w.watches.byUser {
		w.remove(name)
	}
	w.mu.Unlock()

	<-w.doneResp // Wait for readEvents() to finish.
	return nil
}

func (w *inotify) Add(name string) error { return w.AddWith(name) }

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

	add := func(path string, with withOpts, owners ...string) error {
		var flags uint32
		if with.op.Has(Create) {
			flags |= unix.IN_CREATE
		}
		if with.op.Has(Write) {
			flags |= unix.IN_MODIFY
		}
		if with.op.Has(Remove) {
			flags |= unix.IN_DELETE | unix.IN_DELETE_SELF
		}
		if with.op.Has(Rename) {
			flags |= unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_MOVE_SELF
		}
		if with.op.Has(Chmod) {
			flags |= unix.IN_ATTRIB
		}
		if with.op.Has(xUnportableOpen) {
			flags |= unix.IN_OPEN
		}
		if with.op.Has(xUnportableRead) {
			flags |= unix.IN_ACCESS
		}
		if with.op.Has(xUnportableCloseWrite) {
			flags |= unix.IN_CLOSE_WRITE
		}
		if with.op.Has(xUnportableCloseRead) {
			flags |= unix.IN_CLOSE_NOWRITE
		}
		return w.register(path, flags, owners)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	path, recurse := recursivePath(path)
	_, existed := w.watches.byUser[path]
	w.watches.addUser(path, recurse)
	rollback := func() {
		if existed {
			return
		}
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
			if with.sendCreate && root != path {
				w.sendEvent(Event{Name: root, Op: Create})
			}

			return add(root, with, path)
		})
		if err != nil {
			rollback()
		}
		return err
	}

	err := add(path, with, path)
	if err != nil {
		rollback()
	}
	return err
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
	for _, owner := range owners {
		w.watches.addOwner(path, owner)
	}
	return nil
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
	return w.remove(filepath.Clean(name))
}

func (w *inotify) remove(name string) error {
	name, recursive := recursivePath(name)
	wds, err := w.watches.releaseOwner(name, recursive)
	if err != nil {
		return err
	}

	for _, wd := range wds {
		_, err := unix.InotifyRmWatch(w.fd, wd)
		if err != nil {
			// TODO: Perhaps it's not helpful to return an error here in every
			// case; the only two possible errors are:
			//
			// EBADF, which happens when w.fd is not a valid file descriptor of
			// any kind.
			//
			// EINVAL, which is when fd is not an inotify descriptor or wd is
			// not a valid watch descriptor. Watch descriptors are invalidated
			// when they are removed explicitly or implicitly; explicitly by
			// inotify_rm_watch, implicitly when the file they are watching is
			// deleted.
			return err
		}
	}
	return nil
}

func (w *inotify) WatchList() []string {
	if w.isClosed() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
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
		close(w.doneResp)
		close(w.Errors)
		close(w.Events)
	}()

	var buf [unix.SizeofInotifyEvent * 4096]byte // Buffer for a maximum of 4096 raw events
	for {
		if w.isClosed() {
			return
		}

		n, err := w.inotifyFile.Read(buf[:])
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return
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

			ev, ok := w.handleEvent(inEvent, &buf, offset)
			if !ok {
				return
			}
			if !w.sendEvent(ev) {
				return
			}

			// Move to the next event in the buffer
			offset += unix.SizeofInotifyEvent + inEvent.Len
		}
	}
}

func (w *inotify) handleEvent(inEvent *unix.InotifyEvent, buf *[65536]byte, offset uint32) (Event, bool) {
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
		return Event{}, true
	}

	var (
		name    = watch.path
		nameLen = uint32(inEvent.Len)
	)
	if nameLen > 0 {
		name += "/" + inotifyEventName(buf, offset, nameLen)
	}

	if debug {
		internal.Debug(name, inEvent.Mask, inEvent.Cookie)
	}

	if inEvent.Mask&unix.IN_IGNORED != 0 || inEvent.Mask&unix.IN_UNMOUNT != 0 {
		w.watches.remove(watch)
		return Event{}, true
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
			return Event{}, true
		}

		err := w.remove(watch.path)
		if err != nil && !errors.Is(err, ErrNonExistentWatch) {
			if !w.sendError(err) {
				return Event{}, false
			}
		}

		if watch.recurse() {
			return Event{Name: watch.path, Op: Rename}, true
		}
	}

	/// Skip if we're watching both this path and the parent; the parent will
	/// already send a delete so no need to do it twice.
	if inEvent.Mask&unix.IN_DELETE_SELF != 0 {
		_, ok := w.watches.path[filepath.Dir(watch.path)]
		if ok {
			return Event{}, true
		}
	}

	ev := w.newEvent(name, inEvent.Mask, inEvent.Cookie)
	// Need to update watch path for recurse.
	if watch.recurse() {
		isDir := inEvent.Mask&unix.IN_ISDIR == unix.IN_ISDIR
		/// New directory created: set up watch on it.
		if isDir && ev.Has(Create) {
			// Directory rename, so we need to update all the children.
			//
			// TODO: this is of course pretty slow; we should use a better data
			// structure for storing all of this, e.g. store children in the
			// watch. I have some code for this in my kqueue refactor we can use
			// in the future. For now I'm okay with this as it's not publicly
			// available. Correctness first, performance second.
			if ev.renamedFrom != "" {
				w.watches.rebase(ev.renamedFrom, ev.Name)
			} else {
				owners := w.watches.recursiveOwners(watch.path)
				err := w.register(ev.Name, watch.flags, owners)
				if !w.sendError(err) {
					return Event{}, false
				}
			}
		}
	}

	return ev, true
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
