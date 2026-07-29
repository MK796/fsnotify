# Recursive watch validation

Recursive watches are enabled through `Watcher.Add` by appending `/...` to a
directory path. Existing descendants and directories created after the watch is
added are watched automatically. `Watcher.Remove` uses the root path without
the `/...` suffix.

The recursive contract is implemented and tested for all native backend groups:

| Backend | Platforms | Coverage |
| --- | --- | --- |
| inotify | Linux amd64 and arm64 | Ownership, move correlation, rollback, removal, close, race, overflow, stress, and exhaustion |
| IOCP | Windows | Ownership, rename, removal, asynchronous I/O lifecycle, close, race, backpressure, buffer variants, stress, and overflow handling |
| kqueue | macOS and BSD | Ownership, rename, move-out cleanup, rollback, removal, close, race where supported, and backpressure |
| FEN | illumos | Ownership, rename, removal, close, backpressure, and recursive lifecycle |

The common contract covers existing and newly created descendants, create,
write, rename, move, remove, overlapping roots, `WatchList`, explicit removal,
and close while consumers are backpressured.

Coverage is platform-equivalent rather than artificially identical. The Go race
detector is used on supported platforms; it is not available on illumos and is
not reliable or supported by all BSD runners. Queue overflow is asserted only
where the native backend reports an overflow condition.

The `recursive backend integration` GitHub Actions workflow is the authoritative
cross-platform validation. A release validation must pass that workflow three
times sequentially on the same commit without retries or source changes.
