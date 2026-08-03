//go:build solaris

// FEN backend for illumos (supported) and Solaris (untested, but should work).
//
// See port_create(3c) etc. for docs. https://www.illumos.org/man/3C/port_create

package fsnotify

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify/internal"
	"golang.org/x/sys/unix"
)

type fen struct {
	*shared
	Events chan Event
	Errors chan error

	doneResp chan struct{}
	opsMu    sync.Mutex // Serializes Add, Remove, event handling, and Close.
	mu       sync.Mutex
	port     *unix.EventPort
	dirs     map[string]Op                  // Associated directories.
	watches  map[string]Op                  // Associated non-directories.
	byUser   map[string]Op                  // Paths added through Add().
	recurse  map[string]Op                  // Recursive roots → Op filter.
	owners   map[string]map[string]struct{} // Associated path → Add() roots.
	info     map[string]os.FileInfo         // Last identity associated with a path.

	renames     [10]fenRename
	renameIndex uint8
}

type fenRename struct {
	info os.FileInfo
}

var defaultBufferSize = 0

func newBackend(ev chan Event, errs chan error) (backend, error) {
	w := &fen{
		shared:   newShared(ev, errs),
		Events:   ev,
		Errors:   errs,
		doneResp: make(chan struct{}),
		dirs:     make(map[string]Op),
		watches:  make(map[string]Op),
		byUser:   make(map[string]Op),
		recurse:  make(map[string]Op),
		owners:   make(map[string]map[string]struct{}),
		info:     make(map[string]os.FileInfo),
	}

	var err error
	w.port, err = unix.NewEventPort()
	if err != nil {
		return nil, fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}

	go w.readEvents()
	return w, nil
}

func (w *fen) Close() error {
	// Publish shutdown before waiting for an in-flight event handler. This
	// releases handlers blocked while sending to an unconsumed public channel.
	alreadyClosed := w.shared.close()
	if !alreadyClosed {
		w.opsMu.Lock()
		err := w.port.Close()
		w.opsMu.Unlock()
		if err != nil {
			return err
		}
	}

	<-w.doneResp
	return nil
}

func (w *fen) Add(name string) error { return w.AddWith(name) }

func (w *fen) userWatch(path string) (Op, bool, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	op, ok := w.byUser[path]
	_, recursive := w.recurse[path]
	return op, recursive, ok
}

func (w *fen) addUserWatch(path string, recursive bool, op Op) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.byUser[path] = op
	if _, isDir := w.dirs[path]; !isDir {
		w.watches[path] = op
	}
	if recursive {
		w.recurse[path] = op
	}
}

func (w *fen) associateOwned(path string, stat os.FileInfo, follow bool, op Op, owners []string, scanDir bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.associateFileLocked(path, stat, follow); err != nil {
		return err
	}

	if stat.IsDir() && scanDir {
		w.dirs[path] = op
	}
	if stat.IsDir() {
		w.info[path] = stat
	}
	pathOwners := w.owners[path]
	if pathOwners == nil {
		pathOwners = make(map[string]struct{}, len(owners))
		w.owners[path] = pathOwners
	}
	for _, owner := range owners {
		pathOwners[owner] = struct{}{}
	}
	return nil
}

func (w *fen) ownersFor(path string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	owners := w.owners[path]
	out := make([]string, 0, len(owners))
	for owner := range owners {
		out = append(out, owner)
	}
	return out
}

func (w *fen) recursiveOwnersFor(path string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	owners := w.owners[path]
	out := make([]string, 0, len(owners))
	for owner := range owners {
		if _, ok := w.recurse[owner]; ok {
			out = append(out, owner)
		}
	}
	return out
}

func (w *fen) opForOwners(owners []string) Op {
	w.mu.Lock()
	defer w.mu.Unlock()
	var op Op
	for _, owner := range owners {
		op |= w.byUser[owner]
	}
	return op
}

func (w *fen) tracksDirectory(path string, stat os.FileInfo) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	previous := w.info[path]
	return previous != nil && os.SameFile(previous, stat)
}

func (w *fen) releaseOwner(owner string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.byUser, owner)
	delete(w.recurse, owner)
	delete(w.watches, owner)

	var unused []string
	for path, owners := range w.owners {
		delete(owners, owner)
		if len(owners) == 0 {
			unused = append(unused, path)
			continue
		}

		// A directory may already be associated through a non-recursive
		// parent watch when a recursive Add temporarily upgrades it to a
		// directory scan. Restore that distinction when the recursive owner
		// is released instead of retaining recursive scan state.
		if !w.directoryNeedsScanLocked(path, owners) {
			delete(w.dirs, path)
		}
	}
	sort.Slice(unused, func(i, j int) bool {
		return len(unused[i]) > len(unused[j])
	})

	var firstErr error
	for _, path := range unused {
		delete(w.dirs, path)
		delete(w.watches, path)
		delete(w.owners, path)
		delete(w.info, path)
		if err := w.dissociatePathLocked(path); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// directoryNeedsScanLocked reports whether path must be scanned for new
// children. w.mu must be held.
func (w *fen) directoryNeedsScanLocked(path string, owners map[string]struct{}) bool {
	info := w.info[path]
	if info == nil || !info.IsDir() {
		return false
	}
	if _, explicit := w.byUser[path]; explicit {
		return true
	}
	for owner := range owners {
		if _, recursive := w.recurse[owner]; recursive {
			return true
		}
	}
	return false
}

func (w *fen) dropPhysical(path string, tree bool) {
	w.mu.Lock()
	var removedUser []string
	for tracked := range w.owners {
		if tracked == path || (tree && hasPathPrefix(tracked, path)) {
			delete(w.dirs, tracked)
			delete(w.watches, tracked)
			delete(w.owners, tracked)
			delete(w.info, tracked)
			if tracked != path {
				_ = w.dissociatePathLocked(tracked)
			}
		}
	}
	for userPath := range w.byUser {
		if userPath == path || (tree && hasPathPrefix(userPath, path)) {
			if userPath != path {
				removedUser = append(removedUser, userPath)
			}
			delete(w.byUser, userPath)
			delete(w.recurse, userPath)
		}
	}
	w.mu.Unlock()

	sort.Slice(removedUser, func(i, j int) bool {
		return len(removedUser[i]) > len(removedUser[j])
	})
	for _, removed := range removedUser {
		if !w.sendEvent(Event{Name: removed, Op: Remove}) {
			return
		}
	}
}

func (w *fen) rememberRename(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var recursiveOwner bool
	for owner := range w.owners[path] {
		if owner == path {
			continue
		}
		if _, ok := w.recurse[owner]; ok {
			recursiveOwner = true
			break
		}
	}
	if !recursiveOwner || w.info[path] == nil {
		return
	}

	w.renames[w.renameIndex] = fenRename{info: w.info[path]}
	w.renameIndex = (w.renameIndex + 1) % uint8(len(w.renames))
}

func (w *fen) takeRename(info os.FileInfo) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for offset := 1; offset <= len(w.renames); offset++ {
		i := (int(w.renameIndex) - offset + len(w.renames)) % len(w.renames)
		rename := w.renames[i]
		if rename.info == nil || !os.SameFile(rename.info, info) {
			continue
		}
		w.renames[i] = fenRename{}
		return true
	}
	return false
}

func (w *fen) AddWith(name string, opts ...addOpt) error {
	w.opsMu.Lock()
	defer w.opsMu.Unlock()

	if w.isClosed() {
		return ErrClosed
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  AddWith(%q)\n",
			time.Now().Format("15:04:05.000000000"), name)
	}

	with := getOptions(opts...)
	if !w.xSupports(with.op) {
		return fmt.Errorf("%w: %s", xErrUnsupported, with.op)
	}

	name, recurse := recursivePath(name)
	existingOp, existingRecursive, exists := w.userWatch(name)
	if exists {
		// EventPort associations are one-shot. Refresh an existing logical
		// watch so Add remains idempotent even while an event is awaiting rearm.
		with.op = existingOp
		with.sendCreate = false
		recurse = existingRecursive
	}
	if recurse {
		return w.addRecursive(name, with, !exists)
	}

	stat, err := os.Stat(name)
	if err != nil {
		return err
	}

	if stat.IsDir() {
		err := w.handleDirectory(name, stat, true, func(path string, stat os.FileInfo, follow bool) error {
			return w.associateOwned(path, stat, follow, with.op, []string{name}, path == name)
		})
		if err != nil {
			if !exists {
				_ = w.releaseOwner(name)
			}
			return err
		}
		w.addUserWatch(name, false, with.op)
		return nil
	}

	err = w.associateOwned(name, stat, true, with.op, []string{name}, false)
	if err != nil {
		return err
	}
	w.addUserWatch(name, false, with.op)
	return nil
}

func (w *fen) addRecursive(name string, with withOpts, rollbackOnError bool) error {
	err := filepath.WalkDir(name, func(root string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if root == name && !d.IsDir() {
			return fmt.Errorf("fsnotify: not a directory: %q", name)
		}
		stat, err := d.Info()
		if err != nil {
			return err
		}
		if err := w.associateOwned(root, stat, root == name, with.op, []string{name}, d.IsDir()); err != nil {
			return err
		}
		if with.sendCreate && root != name && d.IsDir() {
			w.sendEvent(Event{Name: root, Op: Create})
		}
		return nil
	})
	if err != nil {
		if rollbackOnError {
			_ = w.releaseOwner(name)
		}
		return err
	}
	w.addUserWatch(name, true, with.op)
	return nil
}

func (w *fen) Remove(name string) error {
	w.opsMu.Lock()
	defer w.opsMu.Unlock()

	if w.isClosed() {
		return nil
	}

	name, recurse := recursivePath(name)

	w.mu.Lock()
	_, isRecurse := w.recurse[name]
	_, byUser := w.byUser[name]
	w.mu.Unlock()

	if recurse && !isRecurse {
		return fmt.Errorf("can't use /... with non-recursive watch %q", name)
	}

	if !byUser {
		return fmt.Errorf("%w: %s", ErrNonExistentWatch, name)
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  Remove(%q)\n",
			time.Now().Format("15:04:05.000000000"), name)
	}

	return w.releaseOwner(name)
}

// readEvents contains the main loop that runs in a goroutine watching for events.
func (w *fen) readEvents() {
	// If this function returns, the watcher has been closed and we can close
	// these channels
	defer func() {
		close(w.Errors)
		close(w.Events)
		close(w.doneResp)
	}()

	pevents := make([]unix.PortEvent, 8)
	for {
		count, err := internal.IgnoringEINTR(func() (int, error) {
			return w.port.Get(pevents, 1, nil)
		})
		if err != nil && err != unix.ETIME {
			// EventPort.Get may return either EBADF or an x/sys wrapper error
			// when Close races with completion processing. Once the watcher is
			// closed, neither form is a user-visible read error.
			if w.isClosed() {
				return
			}
			// Interrupted system call (count should be 0) ignore and continue
			if errors.Is(err, unix.EINTR) && count == 0 {
				continue
			}
			// There was an error not caused by calling w.Close()
			if !w.sendError(fmt.Errorf("port.Get: %w", err)) {
				return
			}
		}

		p := pevents[:count]
		for _, pevent := range p {
			if pevent.Source != unix.PORT_SOURCE_FILE {
				// Event from unexpected source received; should never happen.
				if !w.sendError(errors.New("Event from unexpected source received")) {
					return
				}
				continue
			}

			if debug {
				internal.Debug(pevent.Path, pevent.Events)
			}

			err = w.handleEvent(&pevent)
			if !w.sendError(err) {
				return
			}
		}
	}
}

func (w *fen) handleDirectory(path string, stat os.FileInfo, follow bool, handler func(string, os.FileInfo, bool) error) error {
	files, err := os.ReadDir(path)
	if err != nil {
		return err
	}

	// Handle all children of the directory.
	for _, entry := range files {
		finfo, err := entry.Info()
		if err != nil {
			return err
		}
		err = handler(filepath.Join(path, finfo.Name()), finfo, false)
		if err != nil {
			return err
		}
	}

	// And finally handle the directory itself.
	return handler(path, stat, follow)
}

// handleEvent might need to emit more than one fsnotify event if the events
// bitmap matches more than one event type (e.g. the file was both modified and
// had the attributes changed between when the association was created and the
// when event was returned)
func (w *fen) handleEvent(event *unix.PortEvent) error {
	w.opsMu.Lock()
	defer w.opsMu.Unlock()

	// Close can unblock EventPort.Get before this queued event reaches the
	// handler. Such an event must not restore associations or ownership.
	if w.isClosed() {
		return nil
	}

	var (
		events     = event.Events
		path       = event.Path
		fmode      = event.Cookie.(os.FileMode)
		reRegister = true
		dropTree   = false
	)

	w.mu.Lock()
	_, watchedDir := w.dirs[path]
	_, watchedPath := w.watches[path]
	_, tracked := w.owners[path]
	w.mu.Unlock()
	if !tracked {
		// EventPort associations are one-shot. A queued event can outlive the
		// final owner; it must not restore an association removed by Remove.
		return nil
	}
	isWatched := watchedDir || watchedPath

	if events&unix.FILE_DELETE != 0 {
		if !w.sendEvent(Event{Name: path, Op: Remove}) {
			return nil
		}
		reRegister = false
	}
	if events&unix.FILE_RENAME_FROM != 0 {
		if !w.sendEvent(Event{Name: path, Op: Rename}) {
			return nil
		}
		if fmode.IsDir() {
			w.rememberRename(path)
			dropTree = true
		}
		// Don't keep watching the new file name
		reRegister = false
	}
	if events&unix.FILE_RENAME_TO != 0 {
		// We don't report a Rename event for this case, because Rename events
		// are interpreted as referring to the _old_ name of the file, and in
		// this case the event would refer to the new name of the file. This
		// type of rename event is not supported by fsnotify.

		// inotify reports a Remove event in this case, so we simulate this
		// here.
		if !w.sendEvent(Event{Name: path, Op: Remove}) {
			return nil
		}
		dropTree = fmode.IsDir()
		// Don't keep watching the file that was removed
		reRegister = false
	}

	// The file is gone, nothing left to do.
	if !reRegister {
		// FEN associations are one-shot. A parent DELETE can be delivered
		// before pending child DELETE events; dissociating the entire tree here
		// would discard children whose events have not fired yet. A rename is
		// different: descendants left the watched namespace with their parent
		// and must be removed immediately.
		w.dropPhysical(path, dropTree)
		return nil
	}

	// If we didn't get a deletion the file still exists and we're going to have
	// to watch it again. Let's Stat it now so that we can compare permissions
	// and have what we need to continue watching the file

	stat, err := os.Lstat(path)
	if err != nil {
		// This is unexpected, but we should still emit an event. This happens
		// most often on "rm -r" of a subdirectory inside a watched directory We
		// get a modify event of something happening inside, but by the time we
		// get here, the sudirectory is already gone. Clearly we were watching
		// this path but now it is gone. Let's tell the user that it was
		// removed.
		if !w.sendEvent(Event{Name: path, Op: Remove}) {
			return nil
		}
		// Preserve child associations for the same reason as FILE_DELETE
		// above: each child still needs to deliver its own removal event.
		w.dropPhysical(path, false)
		// Suppress extra write events on removed directories; they are not
		// informative and can be confusing.
		return nil
	}

	// resolve symlinks that were explicitly watched as we would have at Add()
	// time. this helps suppress spurious Chmod events on watched symlinks
	if isWatched {
		stat, err = os.Stat(path)
		if err != nil {
			// The symlink still exists, but the target is gone. Report the
			// Remove similar to above.
			if !w.sendEvent(Event{Name: path, Op: Remove}) {
				return nil
			}
			w.dropPhysical(path, false)
			return nil
		}
	}

	// EventPort associations are one-shot. Rearm before publishing events or
	// scanning directories so a racing change either queues the next event or
	// appears in the scan; handling first leaves an unobservable gap.
	err = w.associateFile(path, stat, isWatched)
	if errors.Is(err, fs.ErrNotExist) {
		// Preserve child associations: pending child events still have to clear
		// their own physical and logical state.
		w.dropPhysical(path, false)
		return nil
	}
	if err != nil {
		return err
	}

	// A directory association may have been consumed by any non-terminal
	// event. Reconcile after rearming regardless of the event bit: a child
	// created during the one-shot gap cannot queue its own directory event.
	if fmode.IsDir() && watchedDir {
		if err := w.updateDirectory(path); err != nil {
			return err
		}
	} else if events&unix.FILE_MODIFIED != 0 {
		if !w.sendEvent(Event{Name: path, Op: Write}) {
			return nil
		}
	}
	if events&unix.FILE_ATTRIB != 0 && stat != nil {
		// Only send Chmod if perms changed
		if stat.Mode().Perm() != fmode.Perm() {
			if !w.sendEvent(Event{Name: path, Op: Chmod}) {
				return nil
			}
		}
	}

	return nil
}

// The directory was modified, so we must find unwatched entities and watch
// them. If something was removed from the directory, nothing will happen, as
// everything else should still be watched.
func (w *fen) updateDirectory(path string) error {
	files, err := os.ReadDir(path)
	if err != nil {
		// Directory no longer exists: probably just deleted since we got the
		// event.
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	owners := w.ownersFor(path)
	recursiveOwners := w.recursiveOwnersFor(path)
	for _, entry := range files {
		entryPath := filepath.Join(path, entry.Name())
		if w.port.PathIsWatched(entryPath) {
			continue
		}

		finfo, err := entry.Info()
		if err != nil {
			return err
		}

		// FEN associations are one-shot. Once an event has fired,
		// PathIsWatched reports false before the queued event has been handled
		// and the association has been restored. The recursive parent scan
		// must not report that same directory as newly created again.
		if finfo.IsDir() && w.tracksDirectory(entryPath, finfo) {
			continue
		}

		if finfo.IsDir() && len(recursiveOwners) > 0 {
			if w.takeRename(finfo) {
				if err := w.addRenamedSubdir(entryPath, owners, recursiveOwners); err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						continue
					}
					if !w.sendError(err) {
						return nil
					}
				}
				if !w.sendEvent(Event{Name: entryPath, Op: Create}) {
					return nil
				}
				continue
			}
			if err := w.addRecursiveSubdir(entryPath, owners, recursiveOwners); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if !w.sendError(err) {
					return nil
				}
			}
			continue
		}

		err = w.associateOwned(entryPath, finfo, false, w.opForOwners(owners), owners, false)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if !w.sendError(err) {
			return nil
		}
		if !w.sendEvent(Event{Name: entryPath, Op: Create}) {
			return nil
		}
	}
	return nil
}

func (w *fen) addRenamedSubdir(root string, rootOwners, recursiveOwners []string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		stat, err := d.Info()
		if err != nil {
			return err
		}
		owners := recursiveOwners
		if path == root {
			owners = rootOwners
		}
		return w.associateOwned(path, stat, false, w.opForOwners(owners), owners, d.IsDir())
	})
}

func (w *fen) addRecursiveSubdir(root string, rootOwners, recursiveOwners []string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		stat, err := d.Info()
		if err != nil {
			return err
		}
		owners := recursiveOwners
		if path == root {
			owners = rootOwners
		}
		if err := w.associateOwned(path, stat, false, w.opForOwners(owners), owners, d.IsDir()); err != nil {
			return err
		}
		if !w.sendEvent(Event{Name: path, Op: Create}) {
			return nil
		}
		return nil
	})
}

func (w *fen) associateFile(path string, stat os.FileInfo, follow bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.associateFileLocked(path, stat, follow)
}

func (w *fen) associateFileLocked(path string, stat os.FileInfo, follow bool) error {
	if w.isClosed() {
		return ErrClosed
	}

	if err := w.dissociatePathLocked(path); err != nil {
		return err
	}

	var events int
	if !follow {
		// Watch symlinks themselves rather than their targets unless this entry
		// is explicitly watched.
		events |= unix.FILE_NOFOLLOW
	}
	if true { // TODO: implement withOps()
		events |= unix.FILE_MODIFIED
	}
	if true {
		events |= unix.FILE_ATTRIB
	}
	err := w.port.AssociatePath(path, stat, events, stat.Mode())
	if err != nil {
		return fmt.Errorf("port.AssociatePath(%q): %w", path, err)
	}
	return nil
}

// dissociatePathLocked requires w.mu. EventPort.Get can consume a one-shot
// association between PathIsWatched and DissociatePath. In that case the
// association is already gone and the failed dissociation is successful from
// the watcher's perspective.
func (w *fen) dissociatePathLocked(path string) error {
	if !w.port.PathIsWatched(path) {
		return nil
	}
	err := w.port.DissociatePath(path)
	if err == nil || errors.Is(err, unix.ENOENT) || !w.port.PathIsWatched(path) {
		return nil
	}
	return fmt.Errorf("port.DissociatePath(%q): %w", path, err)
}

func (w *fen) dissociateFile(path string, stat os.FileInfo, unused bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dissociatePathLocked(path)
}

func (w *fen) WatchList() []string {
	if w.isClosed() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	entries := make([]string, 0, len(w.byUser))
	for pathname := range w.byUser {
		entries = append(entries, pathname)
	}

	return entries
}

func (w *fen) xSupports(op Op) bool {
	if op.Has(xUnportableOpen) || op.Has(xUnportableRead) ||
		op.Has(xUnportableCloseWrite) || op.Has(xUnportableCloseRead) {
		return false
	}
	return true
}
