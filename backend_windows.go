//go:build windows

// Windows backend based on ReadDirectoryChangesW()
//
// https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-readdirectorychangesw

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/fsnotify/fsnotify/internal"
	"golang.org/x/sys/windows"
)

type readDirChangesW struct {
	Events chan Event
	Errors chan error

	port         windows.Handle    // Handle to completion port
	input        chan *input       // Inputs to the reader are sent on this channel
	done         chan struct{}     // Closed to unblock event and error sends
	closeRequest chan chan<- error // Prioritized shutdown request
	closeDone    chan struct{}     // Closed after the I/O thread stopped

	mu       sync.Mutex // Protects access to watches, closed
	watches  watchMap   // Map of watches (key: i-number)
	closed   bool       // Set to true when Close() is first called
	closeErr error      // Set before closeDone is closed

	// Accessed only by the I/O thread. Every asynchronous read owns a distinct
	// OVERLAPPED and buffer until its completion has been dequeued.
	pending map[*windows.Overlapped]*watchOperation
}

var defaultBufferSize = 50

func newBackend(ev chan Event, errs chan error) (backend, error) {
	port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 0)
	if err != nil {
		return nil, os.NewSyscallError("CreateIoCompletionPort", err)
	}
	w := &readDirChangesW{
		Events:       ev,
		Errors:       errs,
		port:         port,
		watches:      make(watchMap),
		pending:      make(map[*windows.Overlapped]*watchOperation),
		input:        make(chan *input, 1),
		done:         make(chan struct{}),
		closeRequest: make(chan chan<- error, 1),
		closeDone:    make(chan struct{}),
	}
	go w.readEvents()
	return w, nil
}

func (w *readDirChangesW) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func (w *readDirChangesW) markClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	w.closed = true
	return true
}

func (w *readDirChangesW) sendEvent(name, renamedFrom string, mask uint64) bool {
	if mask == 0 {
		return false
	}

	event := w.newEvent(name, uint32(mask))
	event.renamedFrom = renamedFrom
	select {
	case <-w.done:
		return false
	case w.Events <- event:
		return true
	}
}

// Returns true if the error was sent, or false if watcher is closed.
func (w *readDirChangesW) sendError(err error) bool {
	if err == nil {
		return true
	}
	select {
	case <-w.done:
		return false
	case w.Errors <- err:
		return true
	}
}

func (w *readDirChangesW) Close() error {
	if !w.markClosed() {
		<-w.closeDone
		return w.closeErr
	}
	close(w.done)

	reply := make(chan error, 1)
	w.closeRequest <- reply
	if err := w.wakeupReader(); err != nil {
		// The I/O thread can consume the close request from an already queued
		// completion and close the port before this explicit wakeup.
		if !errors.Is(err, ErrClosed) {
			w.closeErr = err
			close(w.closeDone)
			return err
		}
	}
	w.closeErr = <-reply
	close(w.closeDone)
	return w.closeErr
}

func (w *readDirChangesW) Add(name string) error { return w.AddWith(name) }

func (w *readDirChangesW) AddWith(name string, opts ...addOpt) error {
	if w.isClosed() {
		return ErrClosed
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  AddWith(%q)\n",
			time.Now().Format("15:04:05.000000000"), filepath.ToSlash(name))
	}

	with := getOptions(opts...)
	if !w.xSupports(with.op) {
		return fmt.Errorf("%w: %s", xErrUnsupported, with.op)
	}
	if with.bufsize < 4096 {
		return fmt.Errorf("fsnotify.WithBufferSize: buffer size cannot be smaller than 4096 bytes")
	}

	in := &input{
		op:      opAddWatch,
		path:    filepath.Clean(name),
		flags:   sysFSALLEVENTS,
		reply:   make(chan error, 1),
		bufsize: with.bufsize,
	}
	select {
	case w.input <- in:
	case <-w.done:
		return ErrClosed
	}
	if err := w.wakeupReader(); err != nil {
		return err
	}
	select {
	case err := <-in.reply:
		return err
	case <-w.done:
		return ErrClosed
	}
}

func (w *readDirChangesW) Remove(name string) error {
	if w.isClosed() {
		return nil
	}
	if debug {
		fmt.Fprintf(os.Stderr, "FSNOTIFY_DEBUG: %s  Remove(%q)\n",
			time.Now().Format("15:04:05.000000000"), filepath.ToSlash(name))
	}

	in := &input{
		op:    opRemoveWatch,
		path:  filepath.Clean(name),
		reply: make(chan error, 1),
	}
	select {
	case w.input <- in:
	case <-w.done:
		return nil
	}
	if err := w.wakeupReader(); err != nil {
		if errors.Is(err, ErrClosed) {
			return nil
		}
		return err
	}
	select {
	case err := <-in.reply:
		return err
	case <-w.done:
		return nil
	}
}

func (w *readDirChangesW) WatchList() []string {
	if w.isClosed() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	entries := make([]string, 0, len(w.watches))
	for _, entry := range w.watches {
		for _, watchEntry := range entry {
			for name := range watchEntry.names {
				entries = append(entries, filepath.Join(watchEntry.path, name))
			}
			// the directory itself is being watched
			if watchEntry.mask != 0 {
				entries = append(entries, watchEntry.path)
			}
		}
	}

	return entries
}

// These options are from the old golang.org/x/exp/winfsnotify, where you could
// add various options to the watch. This has long since been removed.
//
// The "sys" in the name is misleading as they're not part of any "system".
//
// This should all be removed at some point, and just use windows.FILE_NOTIFY_*
const (
	sysFSALLEVENTS  = 0xfff
	sysFSCREATE     = 0x100
	sysFSDELETE     = 0x200
	sysFSDELETESELF = 0x400
	sysFSMODIFY     = 0x2
	sysFSMOVE       = 0xc0
	sysFSMOVEDFROM  = 0x40
	sysFSMOVEDTO    = 0x80
	sysFSMOVESELF   = 0x800
	sysFSIGNORED    = 0x8000
)

func (w *readDirChangesW) newEvent(name string, mask uint32) Event {
	e := Event{Name: name}
	if mask&sysFSCREATE == sysFSCREATE || mask&sysFSMOVEDTO == sysFSMOVEDTO {
		e.Op |= Create
	}
	if mask&sysFSDELETE == sysFSDELETE || mask&sysFSDELETESELF == sysFSDELETESELF {
		e.Op |= Remove
	}
	if mask&sysFSMODIFY == sysFSMODIFY {
		e.Op |= Write
	}
	if mask&sysFSMOVE == sysFSMOVE || mask&sysFSMOVESELF == sysFSMOVESELF || mask&sysFSMOVEDFROM == sysFSMOVEDFROM {
		e.Op |= Rename
	}
	return e
}

const (
	opAddWatch = iota
	opRemoveWatch
)

const (
	provisional uint64 = 1 << (32 + iota)
)

type input struct {
	op      int
	path    string
	flags   uint32
	bufsize int
	reply   chan error
}

type inode struct {
	handle windows.Handle
	volume uint32
	index  uint64
}

type watch struct {
	ino     *inode            // i-number
	recurse bool              // Recursive watch?
	path    string            // Directory path
	mask    uint64            // Directory itself is being watched with these notify flags
	names   map[string]uint64 // Map of names being watched and their notify flags
	rename  string            // Remembers the old name while renaming a file
	bufsize int               // Size used for each asynchronous read buffer
	buf     []byte            // Reused only after the owning read completed
	active  *watchOperation   // Read whose completion has not been dequeued
}

// ov must remain the first field: GetQueuedCompletionStatus returns the
// address passed to ReadDirectoryChangesW, which is also the operation address.
type watchOperation struct {
	ov    windows.Overlapped
	watch *watch
	buf   []byte
}

type (
	indexMap map[uint64]*watch
	watchMap map[uint32]indexMap
)

func (w *readDirChangesW) wakeupReader() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.port == windows.InvalidHandle {
		return ErrClosed
	}
	err := windows.PostQueuedCompletionStatus(w.port, 0, 0, nil)
	if err != nil {
		return os.NewSyscallError("PostQueuedCompletionStatus", err)
	}
	return nil
}

func (w *readDirChangesW) getDir(pathname string) (dir string, err error) {
	attr, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(pathname))
	if err != nil {
		return "", os.NewSyscallError("GetFileAttributes", err)
	}
	if attr&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		dir = pathname
	} else {
		dir, _ = filepath.Split(pathname)
		dir = filepath.Clean(dir)
	}
	return
}

func (w *readDirChangesW) getIno(path string) (ino *inode, err error) {
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(path),
		windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, os.NewSyscallError("CreateFile", err)
	}

	var fi windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &fi)
	if err != nil {
		windows.CloseHandle(h)
		return nil, os.NewSyscallError("GetFileInformationByHandle", err)
	}
	ino = &inode{
		handle: h,
		volume: fi.VolumeSerialNumber,
		index:  uint64(fi.FileIndexHigh)<<32 | uint64(fi.FileIndexLow),
	}
	return ino, nil
}

// Must run within the I/O thread.
func (m watchMap) get(ino *inode) *watch {
	if i := m[ino.volume]; i != nil {
		return i[ino.index]
	}
	return nil
}

// Must run within the I/O thread.
func (m watchMap) set(ino *inode, watch *watch) {
	i := m[ino.volume]
	if i == nil {
		i = make(indexMap)
		m[ino.volume] = i
	}
	i[ino.index] = watch
}

// Must run within the I/O thread.
func (w *readDirChangesW) addWatch(pathname string, flags uint64, bufsize int) error {
	pathname, recurse := recursivePath(pathname)

	dir, err := w.getDir(pathname)
	if err != nil {
		return err
	}
	if recurse && dir != pathname {
		return fmt.Errorf("fsnotify: not a directory: %q", pathname)
	}

	ino, err := w.getIno(dir)
	if err != nil {
		return err
	}
	w.mu.Lock()
	watchEntry := w.watches.get(ino)
	w.mu.Unlock()
	if watchEntry == nil {
		_, err := windows.CreateIoCompletionPort(ino.handle, w.port, 0, 0)
		if err != nil {
			windows.CloseHandle(ino.handle)
			return os.NewSyscallError("CreateIoCompletionPort", err)
		}
		watchEntry = &watch{
			ino:     ino,
			path:    dir,
			names:   make(map[string]uint64),
			recurse: recurse,
			bufsize: bufsize,
			buf:     make([]byte, bufsize),
		}
		w.mu.Lock()
		w.watches.set(ino, watchEntry)
		w.mu.Unlock()
		flags |= provisional
	} else {
		windows.CloseHandle(ino.handle)
	}
	w.mu.Lock()
	if pathname == dir {
		watchEntry.mask |= flags
	} else {
		watchEntry.names[filepath.Base(pathname)] |= flags
	}
	w.mu.Unlock()

	err = w.startRead(watchEntry)
	if err != nil {
		return err
	}

	w.mu.Lock()
	if pathname == dir {
		watchEntry.mask &= ^provisional
	} else {
		watchEntry.names[filepath.Base(pathname)] &= ^provisional
	}
	w.mu.Unlock()
	return nil
}

// Must run within the I/O thread.
func (w *readDirChangesW) remWatch(pathname string) error {
	pathname, recurse := recursivePath(pathname)

	dir, err := w.getDir(pathname)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNonExistentWatch, pathname)
		}
		return err
	}
	ino, err := w.getIno(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNonExistentWatch, pathname)
		}
		return err
	}

	w.mu.Lock()
	watch := w.watches.get(ino)
	w.mu.Unlock()
	if watch == nil {
		windows.CloseHandle(ino.handle)
		return fmt.Errorf("%w: %s", ErrNonExistentWatch, pathname)
	}

	if recurse && !watch.recurse {
		windows.CloseHandle(ino.handle)
		return fmt.Errorf("can't use \\... with non-recursive watch %q", pathname)
	}

	err = windows.CloseHandle(ino.handle)
	if err != nil {
		w.sendError(os.NewSyscallError("CloseHandle", err))
	}
	if pathname == dir {
		w.mu.Lock()
		mask := watch.mask
		watch.mask = 0
		w.mu.Unlock()
		w.sendEvent(watch.path, "", mask&sysFSIGNORED)
	} else {
		name := filepath.Base(pathname)
		w.mu.Lock()
		mask := watch.names[name]
		delete(watch.names, name)
		w.mu.Unlock()
		w.sendEvent(filepath.Join(watch.path, name), "", mask&sysFSIGNORED)
	}

	return w.startRead(watch)
}

// Must run within the I/O thread.
func (w *readDirChangesW) deleteWatch(watch *watch) {
	w.clearWatch(watch, true)
}

// Must run within the I/O thread.
func (w *readDirChangesW) clearWatch(watch *watch, notify bool) {
	// Snapshot+clear under the lock so concurrent WatchList() readers see a
	// consistent state. sendEvent must run outside the lock since it can
	// block on the user-facing Events channel.
	w.mu.Lock()
	names := watch.names
	watch.names = make(map[string]uint64)
	mask := watch.mask
	watch.mask = 0
	w.mu.Unlock()

	if !notify {
		return
	}
	for name, m := range names {
		if m&provisional == 0 {
			w.sendEvent(filepath.Join(watch.path, name), "", m&sysFSIGNORED)
		}
	}
	if mask != 0 && mask&provisional == 0 {
		w.sendEvent(watch.path, "", mask&sysFSIGNORED)
	}
}

// Must run within the I/O thread.
func (w *readDirChangesW) removeWatchEntry(watch *watch) {
	w.mu.Lock()
	defer w.mu.Unlock()

	index := w.watches[watch.ino.volume]
	if index == nil || index[watch.ino.index] != watch {
		return
	}
	delete(index, watch.ino.index)
	if len(index) == 0 {
		delete(w.watches, watch.ino.volume)
	}
}

// Must run within the I/O thread and only after the active operation completed.
func (w *readDirChangesW) closeWatch(watch *watch) error {
	if watch.active != nil {
		return errors.New("fsnotify: closing Windows watch with pending I/O")
	}

	handle := watch.ino.handle
	if handle == windows.InvalidHandle {
		w.removeWatchEntry(watch)
		return nil
	}

	// Invalidate before CloseHandle so no later path can close a numerically
	// reused handle.
	watch.ino.handle = windows.InvalidHandle
	w.removeWatchEntry(watch)
	if err := windows.CloseHandle(handle); err != nil {
		return os.NewSyscallError("CloseHandle", err)
	}
	return nil
}

// Must run within the I/O thread. If a read is active, cancellation is
// asynchronous; the replacement read is issued only after its completion is
// dequeued.
func (w *readDirChangesW) startRead(watch *watch) error {
	if watch.active != nil {
		if watch.ino.handle == windows.InvalidHandle {
			return errors.New("fsnotify: Windows watch has pending I/O on an invalid handle")
		}
		err := windows.CancelIoEx(watch.ino.handle, &watch.active.ov)
		if err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
			return os.NewSyscallError("CancelIoEx", err)
		}
		return nil
	}

	// Close can be requested while the I/O thread is processing a completion.
	// Hold mu through ReadDirectoryChanges so marking the backend closed and
	// starting another read have a defined order.
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return w.closeWatch(watch)
	}

	mask := w.toWindowsFlags(watch.mask)
	for _, m := range watch.names {
		mask |= w.toWindowsFlags(m)
	}
	if mask == 0 {
		w.mu.Unlock()
		return w.closeWatch(watch)
	}
	if watch.ino.handle == windows.InvalidHandle {
		w.mu.Unlock()
		return errors.New("fsnotify: starting Windows read on an invalid handle")
	}

	op := &watchOperation{
		watch: watch,
		buf:   watch.buf,
	}
	if len(op.buf) != watch.bufsize {
		op.buf = make([]byte, watch.bufsize)
	}
	watch.buf = nil
	watch.active = op
	w.pending[&op.ov] = op

	err := windows.ReadDirectoryChanges(
		watch.ino.handle,
		unsafe.SliceData(op.buf),
		uint32(len(op.buf)),
		watch.recurse,
		mask,
		nil,
		&op.ov,
		0,
	)
	w.mu.Unlock()
	if err == nil {
		return nil
	}

	delete(w.pending, &op.ov)
	watch.active = nil
	watch.buf = op.buf

	readErr := os.NewSyscallError("ReadDirectoryChanges", err)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// Watched directory was probably removed.
		w.mu.Lock()
		mask := watch.mask
		w.mu.Unlock()
		if mask&provisional == 0 {
			w.sendEvent(watch.path, "", mask&sysFSDELETESELF)
			readErr = nil
		}
	}
	w.deleteWatch(watch)
	if closeErr := w.closeWatch(watch); readErr == nil {
		readErr = closeErr
	}
	return readErr
}

// Must run within the I/O thread. Cancellation is asynchronous: the file
// handle stays valid until the operation's completion has been dequeued.
func (w *readDirChangesW) stopRead(watch *watch) error {
	if watch.active == nil {
		return w.closeWatch(watch)
	}

	if watch.ino.handle == windows.InvalidHandle {
		return errors.New("fsnotify: stopping Windows read on an invalid handle")
	}
	err := windows.CancelIoEx(watch.ino.handle, &watch.active.ov)
	if err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return os.NewSyscallError("CancelIoEx", err)
	}
	return nil
}

// readEvents reads from the I/O completion port, converts the
// received events into Event objects and sends them via the Events channel.
// GetQueuedCompletionStatus and CancelIoEx are not thread-affine, so this
// goroutine must remain free to migrate instead of pinning one OS thread per
// Watcher.
func (w *readDirChangesW) readEvents() {
	var (
		n        uint32
		key      uintptr
		ov       *windows.Overlapped
		closeCh  chan<- error
		closeErr error
	)
	recordCloseError := func(err error) {
		if err != nil && closeErr == nil {
			closeErr = err
		}
	}
	finishClose := func() bool {
		if closeCh == nil || len(w.pending) != 0 {
			return false
		}

		w.mu.Lock()
		var remaining []*watch
		for _, index := range w.watches {
			for _, watch := range index {
				remaining = append(remaining, watch)
			}
		}
		w.mu.Unlock()
		for _, watch := range remaining {
			if watch.active != nil {
				return false
			}
			recordCloseError(w.closeWatch(watch))
		}

		w.mu.Lock()
		handle := w.port
		w.port = windows.InvalidHandle
		err := windows.CloseHandle(handle)
		w.mu.Unlock()
		if err != nil {
			recordCloseError(os.NewSyscallError("CloseHandle", err))
		}
		close(w.Events)
		close(w.Errors)
		closeCh <- closeErr
		return true
	}
	beginClose := func(ch chan<- error) {
		closeCh = ch

		w.mu.Lock()
		var watches []*watch
		for _, index := range w.watches {
			for _, watch := range index {
				watches = append(watches, watch)
			}
		}
		w.mu.Unlock()
		for _, watch := range watches {
			w.clearWatch(watch, false)
			recordCloseError(w.stopRead(watch))
		}
	}
	pollClose := func() {
		if closeCh != nil {
			return
		}
		select {
		case ch := <-w.closeRequest:
			beginClose(ch)
		default:
		}
	}

	for {
		n = 0
		key = 0
		ov = nil
		qErr := windows.GetQueuedCompletionStatus(w.port, &n, &key, &ov, windows.INFINITE)
		pollClose()

		if ov == nil {
			if closeCh != nil {
				select {
				case in := <-w.input:
					if in.op == opRemoveWatch {
						in.reply <- nil
					} else {
						in.reply <- ErrClosed
					}
				default:
				}
				if finishClose() {
					return
				}
				continue
			}

			select {
			case in := <-w.input:
				switch in.op {
				case opAddWatch:
					if w.isClosed() {
						in.reply <- ErrClosed
					} else {
						in.reply <- w.addWatch(in.path, uint64(in.flags), in.bufsize)
					}
				case opRemoveWatch:
					if w.isClosed() {
						in.reply <- nil
					} else {
						in.reply <- w.remWatch(in.path)
					}
				}
			default:
			}
			continue
		}

		op, ok := w.pending[ov]
		if !ok {
			err := errors.New("fsnotify: completion for unknown Windows I/O operation")
			if closeCh != nil {
				recordCloseError(err)
				if finishClose() {
					return
				}
			} else {
				w.sendError(err)
			}
			continue
		}
		delete(w.pending, ov)

		watch := op.watch
		if watch.active != op {
			err := errors.New("fsnotify: completion does not match active Windows I/O operation")
			if closeCh != nil {
				recordCloseError(err)
			} else {
				w.sendError(err)
			}
		} else {
			watch.active = nil
			watch.buf = op.buf
		}

		if closeCh != nil {
			recordCloseError(w.stopRead(watch))
			if finishClose() {
				return
			}
			continue
		}

		switch qErr {
		case nil:
			// No error
		case windows.ERROR_MORE_DATA:
			w.sendError(ErrEventOverflow)
			if err := w.startRead(watch); err != nil {
				w.sendError(err)
			}
			continue
		case windows.ERROR_ACCESS_DENIED:
			// Watched directory was probably removed
			w.mu.Lock()
			mask := watch.mask
			w.mu.Unlock()
			w.sendEvent(watch.path, "", mask&sysFSDELETESELF)
			w.deleteWatch(watch)
			if err := w.closeWatch(watch); err != nil {
				w.sendError(err)
			}
			continue
		case windows.ERROR_OPERATION_ABORTED:
			// A mask change canceled this operation. The current watch state
			// decides whether to issue a replacement or close.
			if err := w.startRead(watch); err != nil {
				w.sendError(err)
			}
			continue
		default:
			w.sendError(os.NewSyscallError("GetQueuedCompletionPort", qErr))
			if err := w.startRead(watch); err != nil {
				w.sendError(err)
			}
			continue
		}

		if n > uint32(len(op.buf)) {
			w.sendError(errors.New("fsnotify: Windows completion exceeds its read buffer"))
			if err := w.startRead(watch); err != nil {
				w.sendError(err)
			}
			continue
		}

		var offset uint32
		for {
			if n == 0 {
				w.sendError(ErrEventOverflow)
				break
			}

			headerSize := uint32(unsafe.Offsetof(windows.FileNotifyInformation{}.FileName))
			if offset > n || n-offset < headerSize {
				w.sendError(errors.New("fsnotify: truncated Windows notification header"))
				break
			}

			// Point "raw" to the event in the buffer
			raw := (*windows.FileNotifyInformation)(unsafe.Pointer(&op.buf[offset]))
			if raw.FileNameLength%2 != 0 || raw.FileNameLength > n-offset-headerSize {
				w.sendError(errors.New("fsnotify: invalid Windows notification filename length"))
				break
			}

			// Create a buf that is the size of the path name
			size := int(raw.FileNameLength / 2)
			buf := unsafe.Slice(&raw.FileName, size)
			name := windows.UTF16ToString(buf)
			fullname := filepath.Join(watch.path, name)

			if debug {
				internal.Debug(fullname, raw.Action)
			}

			var mask uint64
			switch raw.Action {
			case windows.FILE_ACTION_REMOVED:
				mask = sysFSDELETESELF
			case windows.FILE_ACTION_MODIFIED:
				mask = sysFSMODIFY
			case windows.FILE_ACTION_RENAMED_OLD_NAME:
				watch.rename = name
			case windows.FILE_ACTION_RENAMED_NEW_NAME:
				// Update saved path of all sub-watches and rename the
				// names entry under the lock so WatchList() can't observe
				// a torn state.
				old := filepath.Join(watch.path, watch.rename)
				w.mu.Lock()
				for _, watchMap := range w.watches {
					for _, ww := range watchMap {
						if hasPathPrefix(ww.path, old) {
							ww.path = filepath.Join(fullname, strings.TrimPrefix(ww.path, old))
						}
					}
				}
				if watch.names[watch.rename] != 0 {
					watch.names[name] |= watch.names[watch.rename]
					delete(watch.names, watch.rename)
					mask = sysFSMOVESELF
				}
				w.mu.Unlock()
			}

			w.mu.Lock()
			nameMask := watch.names[name]
			watchMask := watch.mask
			w.mu.Unlock()
			if raw.Action != windows.FILE_ACTION_RENAMED_NEW_NAME {
				w.sendEvent(fullname, "", nameMask&mask)
			}
			if raw.Action == windows.FILE_ACTION_REMOVED {
				w.mu.Lock()
				ignored := watch.names[name] & sysFSIGNORED
				delete(watch.names, name)
				w.mu.Unlock()
				w.sendEvent(fullname, "", ignored)
			}

			if watch.rename != "" && raw.Action == windows.FILE_ACTION_RENAMED_NEW_NAME {
				w.sendEvent(fullname, filepath.Join(watch.path, watch.rename), watchMask&w.toFSnotifyFlags(raw.Action))
			} else {
				w.sendEvent(fullname, "", watchMask&w.toFSnotifyFlags(raw.Action))
			}

			if raw.Action == windows.FILE_ACTION_RENAMED_NEW_NAME {
				w.mu.Lock()
				nameMask = watch.names[name]
				w.mu.Unlock()
				w.sendEvent(filepath.Join(watch.path, watch.rename), "", nameMask&mask)
			}

			// Move to the next event in the buffer
			if raw.NextEntryOffset == 0 {
				break
			}
			if raw.NextEntryOffset < headerSize || raw.NextEntryOffset > n-offset {
				//lint:ignore ST1005 Windows should be capitalized
				w.sendError(errors.New("Windows system assumed buffer larger than it is, events have likely been missed"))
				break
			}
			offset += raw.NextEntryOffset
		}

		if err := w.startRead(watch); err != nil {
			w.sendError(err)
		}
	}
}

func (w *readDirChangesW) toWindowsFlags(mask uint64) uint32 {
	var m uint32
	if mask&sysFSMODIFY != 0 {
		m |= windows.FILE_NOTIFY_CHANGE_LAST_WRITE
	}
	if mask&(sysFSMOVE|sysFSCREATE|sysFSDELETE) != 0 {
		m |= windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME
	}
	return m
}

func (w *readDirChangesW) toFSnotifyFlags(action uint32) uint64 {
	switch action {
	case windows.FILE_ACTION_ADDED:
		return sysFSCREATE
	case windows.FILE_ACTION_REMOVED:
		return sysFSDELETE
	case windows.FILE_ACTION_MODIFIED:
		return sysFSMODIFY
	case windows.FILE_ACTION_RENAMED_OLD_NAME:
		return sysFSMOVEDFROM
	case windows.FILE_ACTION_RENAMED_NEW_NAME:
		return sysFSMOVEDTO
	}
	return 0
}

func (w *readDirChangesW) xSupports(op Op) bool {
	if op.Has(xUnportableOpen) || op.Has(xUnportableRead) ||
		op.Has(xUnportableCloseWrite) || op.Has(xUnportableCloseRead) {
		return false
	}
	return true
}
