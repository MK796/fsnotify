# Recursive watch validation

Recursive watches are enabled through `Watcher.Add` by appending `/...` to a
directory path. Existing descendants and directories created after the watch is
added are watched automatically. `Watcher.Remove` uses the root path without
the `/...` suffix.

The normative control and lifecycle semantics are defined in
[`RECURSIVE_WATCH_CONTRACT.md`](RECURSIVE_WATCH_CONTRACT.md). They are identical
for every supported native backend group:

| Backend | Platforms | Coverage |
| --- | --- | --- |
| inotify | Linux amd64 and arm64 | Ownership, move correlation, rollback, removal, close, race, overflow, stress, and exhaustion |
| IOCP | Windows | Ownership, rename, removal, asynchronous I/O lifecycle, close, race, backpressure, buffer variants, stress, and overflow handling |
| kqueue | macOS and BSD | Ownership, rename, move-out cleanup, rollback, removal, close, race where supported, and backpressure |
| FEN | illumos | Ownership, rename, removal, close, backpressure, and recursive lifecycle |

The common contract covers existing and newly created descendants, create,
write, rename, move, remove, overlapping roots, `WatchList`, explicit removal,
and close while consumers are backpressured.

The Recursive Control Contract is platform-identical. Only raw event
representation, existing optional capabilities, and test-environment
capabilities may differ. Every such difference must be recorded in
`.github/policy/recursive-platform-exceptions.json`, backed by non-recursive
evidence, and accepted by the exception-policy metatest. A platform difference
in coverage, ownership, `WatchList`, rollback, cleanup, concurrency, or close
is always a defect.

The Go race detector is used on supported platforms; it is not available on
illumos and is not reliable or supported by all BSD runners. Queue overflow is
asserted only where the native backend reports an overflow condition.

The `recursive backend integration` GitHub Actions workflow is the authoritative
cross-platform validation. A release validation must pass that workflow three
times sequentially on the same commit without retries or source changes.

The historical immutable implementation reference is tagged
`recursive-watch-validation-v1`. The corrected fencing base is `d323a68`,
which includes the separately validated corrections merged through pull
requests 12, 13, 15, and 17. The contract is frozen separately only after the
policy and contract checks pass on every backend.
