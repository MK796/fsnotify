# Recursive Watch Control Contract

## Status

This document is normative. The key words **MUST**, **MUST NOT**, and **MAY**
state requirements.

The contract applies identically to:

- Linux/inotify
- Windows/IOCP
- macOS and BSD/kqueue
- illumos/FEN

The public recursive syntax is `filepath.Join(root, "...")`. In this document,
`recursive(root)` means that path and `root` means the same path without the
trailing marker.

Platform-specific raw event streams MAY retain differences already proven by
equivalent non-recursive watches. Such differences MUST NOT relax recursive
control state, ownership, lifecycle, coverage, rollback, or cleanup.

## Rules

### RC-001: Accept an existing directory root

`Watcher.Add(recursive(root))` **MUST** accept an existing directory as a
recursive root.

Observable behavior: `Add` succeeds and the root is visible in `WatchList`
without the trailing marker.

Tests: `TestRecursiveContract/add_existing_root`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: none.

### RC-002: Reject invalid roots without state changes

Adding a missing path or a file with recursive syntax **MUST** fail and **MUST
NOT** alter public or internal watcher state.

Observable behavior: `Add` fails, `WatchList` is unchanged, and no coverage or
resource belonging to the rejected root remains.

Tests: `TestRecursiveContract/reject_invalid_roots_atomically`;
`TestRecursiveContractResourceInvariants`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: concrete operating-system error values and text MAY
differ; error class and state effect MUST NOT differ.

### RC-003: Register existing descendants

A successful recursive `Add` **MUST** register every directory already below
the root before the call returns.

Observable behavior: a sentinel operation in every pre-existing descendant is
observed after `Add` returns.

Tests: `TestRecursiveContract/register_existing_descendants`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: additional native events MAY occur; recursive
coverage MUST NOT differ.

### RC-004: Register new and moved-in subtrees

Directories created below a recursive root, including multi-level trees, and
pre-populated trees moved below it **MUST** become recursively registered.

Observable behavior: sentinel operations in the deepest new or moved-in
descendants become observable through bounded eventual assertions.

Tests: `TestRecursiveContract/register_new_subtrees`;
`TestRecursiveContract/move_in_registers_complete_subtree`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: raw directory create or move representation MAY
differ; coverage MUST NOT differ.

### RC-005: Make repeated Add idempotent

Repeatedly adding the same recursive root **MUST** be idempotent and **MUST
NOT** duplicate public roots, ownership, physical watches, or semantic event
observations.

Observable behavior: `WatchList` contains one root and one sentinel operation
produces one semantic observation.

Tests: `TestRecursiveContract/idempotent_add`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: existing native event duplication or coalescing MAY
remain only when proven by a non-recursive equivalent.

### RC-006: Expose only explicit user watches

`WatchList` **MUST** contain only paths explicitly added by the caller.
Recursive roots **MUST** appear without the trailing marker.

Observable behavior: a recursive tree contributes exactly one explicit root to
`WatchList`, plus any independently explicit child or nested root.

Tests: `TestRecursiveContract/watch_list_contains_only_explicit_watches`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: none.

### RC-007: Hide internal subtree watches

Physical or logical watches created only to implement recursion **MUST NOT**
appear in the public `WatchList`.

Observable behavior: growing, moving, or renaming a subtree does not expose
internal descendants in `WatchList`.

Tests: `TestRecursiveContract/internal_subwatches_are_hidden`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: none.

### RC-008: Keep explicit and recursive ownership independent

An explicitly added descendant watch and recursive ownership of the same path
**MUST** remain independent.

Observable behavior: removing the recursive root preserves the explicit child
in `WatchList` and preserves its coverage.

Tests: `TestRecursiveContract/explicit_child_survives_outer_remove`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: none.

### RC-009: Keep overlapping recursive roots independent

Overlapping recursive roots **MUST** have independent ownership.

Observable behavior: removing either root preserves the other root and all
coverage still owned by it, including dynamically created descendants.

Tests: `TestRecursiveContract/overlapping_roots_are_independent`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: none.

### RC-010: Accept equivalent Remove forms

`Watcher.Remove(root)` and `Watcher.Remove(recursive(root))` **MUST** be
equivalent for an active recursive root.

Observable behavior: either form removes exactly the same explicit root and
exclusive ownership.

Tests: `TestRecursiveContract/remove_forms_are_equivalent`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: concrete error text MAY differ; success, error
class, and state effect MUST NOT differ.

### RC-011: Preserve inner ownership during Remove

Removing an outer recursive root **MUST** preserve every explicit child watch
and inner recursive root that still owns coverage.

Observable behavior: retained roots remain in `WatchList` and continue to
observe sentinel operations after the outer root is removed.

Tests: `TestRecursiveContract/explicit_child_survives_outer_remove`;
`TestRecursiveContract/inner_root_survives_outer_remove`;
`TestRecursiveContract/remove_releases_only_exclusive_coverage`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: none.

### RC-012: Clean state after root disappearance

When a recursive root ceases to exist, the watcher **MUST** eventually remove it
from `WatchList` and release its exclusive ownership and resources.

Observable behavior: the root disappears from `WatchList`; a later
`Remove(root)` returns `ErrNonExistentWatch`.

Tests: `TestRecursiveContract/root_disappearance_cleans_state`;
`TestRecursiveContractResourceInvariants`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: raw remove and rename events MAY differ.

### RC-013: Preserve coverage after an internal rename

Renaming a directory within the same recursive tree **MUST** preserve recursive
coverage for that directory and all descendants at their new paths.

Observable behavior: a sentinel below the renamed directory is observed at its
new path through a bounded eventual assertion.

Tests: `TestRecursiveContract/rename_within_tree_preserves_coverage`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: `RenamedFrom`, rename/create pairing, and
additional directory writes MAY differ.

### RC-014: Release obsolete coverage after move-out

Moving a subtree out of a recursive root **MUST** release only coverage and
ownership no longer required by another explicit or recursive root.

Observable behavior: operations outside the tree are no longer observed; any
remaining independent owner continues to function.

Tests: `TestRecursiveContract/move_out_releases_obsolete_coverage`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: raw move or remove representation MAY differ.

### RC-015: Register complete moved-in subtrees

Moving a pre-populated subtree into a recursive root **MUST** register the full
subtree.

Observable behavior: a sentinel in an existing deep descendant becomes
observable through a bounded eventual assertion.

Tests: `TestRecursiveContract/move_in_registers_complete_subtree`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: raw move or create representation MAY differ.

### RC-016: Distinguish new objects at reused paths

The watcher **MUST NOT** confuse a removed or moved filesystem object with a new
object later created at the same path.

Observable behavior: the replacement receives current ownership and coverage;
stale events or handles from the old object do not corrupt it.

Tests: `TestRecursiveContract/reused_path_is_new_object`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: stale raw events permitted by the operating system
MAY be delivered but MUST NOT corrupt control state.

### RC-017: Roll back failed registration atomically

If recursive registration fails after partially processing a tree, the watcher
**MUST** restore the exact pre-call public and internal ownership state.

Observable behavior: the failed root is absent from `WatchList`; pre-existing
watches remain functional; no failed-root coverage or resources remain.

Tests: `TestRecursiveContractRegistrationRollbackCoverage` and the referenced
backend fault-injection tests.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: the injected native failure MAY differ; rollback
state MUST NOT differ.

### RC-018: Release recursive resources

Rollback, `Remove`, automatic root disappearance, and `Close` **MUST** release
all exclusive recursive ownership and operating-system resources.

Observable behavior: deterministic backend resource invariants return to their
pre-operation baseline.

Tests: `TestRecursiveContractResourceInvariants` and backend resource tests.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: resource type differs by backend; zero leaked
recursive ownership and resources does not.

### RC-019: Make concurrent lifecycle operations safe

Concurrent `Add`, `Remove`, `WatchList`, event delivery, and `Close` **MUST
NOT** race, deadlock, panic, or leave invalid state.

Observable behavior: operations complete within a bounded interval under the
race detector where supported and leave deterministic post-close behavior.

Tests: `TestRecursiveContract/concurrent_lifecycle`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: availability of the Go race detector is a test
environment capability, not a semantic exception.

### RC-020: Make repeated Close safe

Calling `Close` more than once **MUST** be safe and bounded.

Observable behavior: every call returns without panic or deadlock and public
channels reach their documented terminal state.

Tests: `TestRecursiveContract/idempotent_close`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: none.

### RC-021: Keep lifecycle operations safe under backpressure

A caller that temporarily does not consume `Events` or `Errors` **MUST NOT**
cause a permanent lifecycle deadlock. `Close` **MUST** remain bounded.

Observable behavior: `Close` completes within the contract deadline while
delivery is backpressured.

Tests: `TestRecursiveContract/backpressure_does_not_deadlock_close`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: native queue size and overflow signaling MAY
differ.

### RC-022: Emit no synthetic management events

Recursive subtree registration and ownership bookkeeping **MUST NOT** emit
public events solely because an internal watch was created, transferred, or
removed.

Observable behavior: an otherwise idle tree produces no public event from
control-plane maintenance alone.

Tests: `TestRecursiveContract/internal_management_is_silent`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: filesystem operations used to trigger registration
MAY produce their normal native events.

### RC-023: Introduce no recursive platform differences

Recursive watching **MUST NOT** introduce a platform difference in control
state, ownership, lifecycle, coverage, rollback, or cleanup.

Observable behavior: the same public contract suite passes unchanged on every
backend without platform branches or skips.

Tests: `TestRecursiveControlContractHasNoPlatformBranches`;
`recursive-contract`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: none for the Recursive Control Contract.

### RC-024: Require non-recursive proof for native differences

A platform-specific recursive event expectation, capability exception, or
test-environment exception **MUST** have executable non-recursive evidence that
demonstrates the same native limitation.

Observable behavior: an exception without reachable, executed, and passing
non-recursive evidence fails the exception-policy gate.

Tests: `TestRecursiveExceptionPolicy`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: only `NATIVE_EVENT`, `NATIVE_CAPABILITY`, and
`TEST_ENVIRONMENT` classifications are permitted.

### RC-025: Preserve stable error classes and state effects

Concrete operating-system errors and text **MAY** differ. Whether an operation
succeeds, its public error class, and its state effect **MUST** be equivalent.

Observable behavior: sentinel errors and `WatchList` transitions are equivalent
on every backend.

Tests: `TestRecursiveContract/error_classes_and_state_effects`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: operating-system error wrapping MAY differ.

### RC-026: Use path-component boundaries

Ownership and removal **MUST** use path-component boundaries. A recursive root
such as `/a` **MUST NOT** own, rename, or remove `/ab`.

Observable behavior: removing or renaming one prefix-like root leaves the
sibling root covered.

Tests: `TestRecursiveContract/prefix_similar_roots_are_independent`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: none.

### RC-027: Preserve non-recursive compatibility

Recursive support **MUST NOT** change existing non-recursive `Add`, `Remove`,
`WatchList`, `Close`, options, sentinel errors, or event delivery.

Observable behavior: the stock non-recursive suite and API-compatibility gate
pass unchanged.

Tests: stock `go test ./...`;
`TestRecursiveContract/non_recursive_compatibility`; `api-compatibility`.

Backends: inotify, IOCP, kqueue, and FEN.

Allowed native limitations: previously documented non-recursive differences
remain authoritative.

## Exception Policy

An exception is valid only when all of the following are true:

1. It is classified as `NATIVE_EVENT`, `NATIVE_CAPABILITY`, or
   `TEST_ENVIRONMENT`.
2. An executable non-recursive test proves that recursive watching did not
   create the difference.
3. The exception records its technical reason, affected platform or backend,
   reproduction test, and documentation reference.
4. It does not alter any recursive control-state assertion.

There is no accepted exception category for the Recursive Control Contract.

## Contract Changes

After a valid `recursive-watch-contract-v1` tag exists on `main`, a contract
change:

- **MUST** be isolated from production code,
- **MUST** be explicitly approved by the user,
- **MUST NOT** record agent-generated approval,
- **MUST** update the normative document and affected common tests together,
- **MUST** receive a new monotonically versioned contract tag, and
- **MUST NOT** weaken an existing guarantee merely to accommodate an
  implementation.
