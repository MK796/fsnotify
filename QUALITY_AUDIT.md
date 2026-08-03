# Recursive Watch Quality Audit

This file is the sole progress and findings ledger for recursive-watch quality
work in this fork.

## Reference

```text
Repository: MK796/fsnotify
Upstream base: 20b1e15
Historical validated implementation: fce4bab
Historical reference tag: recursive-watch-validation-v1
Corrected fencing base: d323a68b31cf4a5043d76f95680777b5fa5f6696
Merged fencing candidate: a9b0c5b675aa16c048f450a815fed8d51e882579
Current audit baseline: b448722b1e2287e40ca350fbf307d3714bb4675b
Working branch: quality/recursive-watch-common-fixes-v1
Contract tag: recursive-watch-contract-v1 at 79d2d54222f090711e72a3062821b5a5e8fb520f
```

Production-code auditing started after the Fencing pull request was merged, the
contract was tagged on `main`, branch protection was activated, and the
incremental audit-ledger policy was validated on pull request 22.

## Entry Format

Every finding uses:

```text
ID:
Severity:
Status:
Contract:
Backend:
Finding:
Evidence:
Decision:
Fix commit:
Validation runs:
```

Allowed ID prefixes:

- `AUDIT-COMMON-*`
- `AUDIT-INOTIFY-*`
- `AUDIT-WIN-*`
- `AUDIT-KQUEUE-*`
- `AUDIT-FEN-*`
- `AUDIT-TEST-*`
- `AUDIT-CI-*`
- `AUDIT-PERF-*`

Allowed severities:

- `BLOCKER`
- `MAJOR`
- `MINOR`

Allowed statuses:

- `OPEN`
- `IN_PROGRESS`
- `RESOLVED`
- `ACCEPTED_NATIVE_EVENT`
- `ACCEPTED_NATIVE_CAPABILITY`
- `ACCEPTED_TEST_ENVIRONMENT`

A Recursive Control Contract difference cannot use an `ACCEPTED_*` status.

## Findings

### Common and public API audit coverage

The first line-by-line pass covered `fsnotify.go`, `shared.go`,
`backend_other.go`, `README.md`, and `CHANGELOG.md` at baseline `30551b4`. The
implementation was compared with upstream base `20b1e15`, the frozen Recursive
Control Contract, the common contract suite, and the successful post-merge
runs `30827555576`, `30827555167`, `30827555608`, and `30827561217`.

Only `fsnotify.go` differs from the upstream base in this scope. `shared.go` and
`backend_other.go` contain no recursive-specific change. The public exported
API remains compatible according to the required `api-compatibility` gate.
The root-specific branch in `hasPathPrefix` is required for filesystem roots
and is covered by `TestHasPathPrefix`; no change is justified there. Backend
ownership and lifecycle implementations are explicitly deferred to their
dedicated inotify, IOCP, kqueue, and FEN audit passes.

This pass produced exactly the two findings below.

### AUDIT-COMMON-001 | MINOR | RESOLVED

ID: `AUDIT-COMMON-001`

Severity: `MINOR`

Status: `RESOLVED`

Contract: `RC-001`, `RC-003`, `RC-004`, `RC-006`, `RC-008`, `RC-009`,
`RC-010`, `RC-011`

Backend: public documentation

Finding: Public documentation is internally inconsistent and does not fully
describe the frozen recursive contract. The README still says that
subdirectories are never watched and that recursion is only on the roadmap.
The `Watcher.Remove` documentation says that all sub-watches are removed,
which omits retained overlapping or explicitly owned watches, and it does not
state clearly that both `root` and `root/...` are equivalent Remove forms. The
unreleased changelog does not identify the newly public recursive behavior.

Evidence: `README.md` FAQ "Are subdirectories watched?"; `Watcher.Add`,
`Watcher.Remove`, and `Watcher.WatchList` comments in `fsnotify.go`;
`CHANGELOG.md`; Recursive Control Contract rules listed above.

Decision: In a separate documentation-only change, make README, Go API
comments, and the unreleased changelog describe the same marker, dynamic
coverage, ownership-preserving Remove behavior, equivalent Remove forms, and
WatchList representation. Do not alter runtime semantics or the frozen
contract.

Fix commit: `docs: align recursive watch documentation`

Validation runs: candidate documentation and policy checks required.

### AUDIT-COMMON-002 | MINOR | OPEN

ID: `AUDIT-COMMON-002`

Severity: `MINOR`

Status: `OPEN`

Contract: `RC-001`, `RC-027`

Backend: common path parsing

Finding: `enableRecurse` remains a package-level mutable variable permanently
initialized to `true`, but no production or test code writes it. Its false
branch in `recursivePath` is therefore dead code left over from the period when
recursive support was test-only. It adds an undocumented alternate state to a
now-unconditional public behavior and unnecessary mutable global state.

Evidence: `fsnotify.go` declarations of `enableRecurse` and `recursivePath`;
repository-wide reference search finds only the declaration and read.

Decision: In a separate production change, remove the variable and dead branch
while preserving path cleaning, marker recognition, all public behavior, and
the complete contract suite.

Fix commit: pending

Validation runs: pending

Before this systematic pass, three production blockers discovered by the
initial Fencing contract run were resolved through pull request 17 and
independently validated before the Fencing work was rebased onto the corrected
production base. The first post-merge Fencing runs then exposed one remaining
FEN observation gap and one asynchronous kqueue shutdown gap. The kqueue
correction is independently green; the first FEN candidate exposed a broader
lifecycle-serialization defect recorded below.

### AUDIT-TEST-001 | MAJOR | RESOLVED

ID: `AUDIT-TEST-001`

Severity: `MAJOR`

Status: `RESOLVED`

Contract: `RC-023`, `RC-024`, `RC-027`

Backend: all

Finding: Recursive script outputs and capability directives need a complete,
machine-checked classification. A file path alone is not evidence that the
non-recursive behavior executes on the affected backend.

Evidence: `testdata/watch-recurse`; `testdata/watch-dir`;
`.github/policy/recursive-platform-exceptions.json`.

Decision: Inventory every recursive platform selector and execute every
referenced non-recursive script on each affected backend.

Fix commit: `test: enforce recursive platform exception policy`

Validation runs: candidate `policy`, `stock-test-gate`, and
`recursive-backend-integration` required.

### AUDIT-TEST-002 | BLOCKER | RESOLVED

ID: `AUDIT-TEST-002`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-024`

Backend: Windows/IOCP

Finding: The existing ancestor-rename scenario is skipped on Windows and
therefore cannot prove that Windows itself rejects renaming an ancestor while a
separately watched descendant holds an open directory handle.

Evidence: `fsnotify_test.go`;
`testdata/watch-dir/rename-nested-watched-dir`.

Decision: Add and execute a non-recursive Windows test that watches only the
descendant and asserts that the filesystem rename operation itself fails.

Fix commit: `test: enforce recursive platform exception policy`

Validation runs: candidate Windows stock and recursive-backend jobs required.

### AUDIT-TEST-003 | MAJOR | RESOLVED

ID: `AUDIT-TEST-003`

Severity: `MAJOR`

Status: `RESOLVED`

Contract: `RC-024`

Backend: Windows/IOCP

Finding: Exact recursive tree-removal transcripts are skipped on Windows. The
exception needs executable non-recursive evidence for the underlying
per-directory `ReadDirectoryChangesW` event behavior.

Evidence: `testdata/watch-recurse/remove-dir`;
`testdata/watch-recurse/remove-watched-dir`;
`testdata/watch-dir/subdir`.

Decision: Execute the non-recursive parent-directory script on Windows and keep
the exception limited to raw event representation. Recursive cleanup and
ownership remain mandatory in the common contract suite.

Fix commit: `test: enforce recursive platform exception policy`

Validation runs: candidate Windows stock and recursive-backend jobs required.

### AUDIT-TEST-004 | BLOCKER | RESOLVED

ID: `AUDIT-TEST-004`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-001` through `RC-027`

Backend: all

Finding: The first common contract candidate referenced a nonexistent rollback
test and did not fully assert existing-descendant coverage, duplicate
registration, both overlapping-root removal directions, channel termination,
concurrent event delivery, or rename isolation for prefix-similar roots.

Evidence: `RECURSIVE_WATCH_CONTRACT.md`; `recursive_contract_test.go`;
backend rollback tests.

Decision: Complete the platform-identical public suite, add backend-native
rollback invariants where registration architecture differs, and add a
metatest that rejects every nonexistent test reference in the normative
contract.

Fix commit: `test: complete recursive contract fencing`

Validation runs: candidate `recursive-contract`, `resource-invariants`, and
`recursive-backend-integration` required.

### AUDIT-CI-001 | MAJOR | RESOLVED

ID: `AUDIT-CI-001`

Severity: `MAJOR`

Status: `RESOLVED`

Contract: `RC-023`, `RC-024`, `RC-027`

Backend: all

Finding: The first policy candidate compared the initial Fencing PR with the
historical validation tag instead of the corrected production base, expected
trailers that differed from the documented commit contract, and allowed an
active full matrix to be cancelled.

Evidence: `.github/scripts/check-recursive-policy.sh`;
`.github/workflows/recursive-backend-integration.yml`;
`RECURSIVE_WATCH_FENCING_PLAN.md`.

Decision: Pin the initial Fencing comparison to the corrected production base.
That base was `b6247e0b` when the gate was first created and is `d323a68` after
the blockers found by pull request 16 were resolved through pull request 17.
Use the documented `Audit:` and `Contract:` trailers, freeze the agent rules,
reject new unconditional test sleeps, and set full-matrix cancellation to
false.

Fix commit: `ci: correct recursive watch governance gates`

Validation runs: candidate `policy`, `api-compatibility`, and
`recursive-backend-integration` required.

### AUDIT-TEST-005 | MAJOR | RESOLVED

ID: `AUDIT-TEST-005`

Severity: `MAJOR`

Status: `RESOLVED`

Contract: `RC-023`, `RC-024`

Backend: Windows/IOCP

Finding: The initial Fencing run rejected `RPE-WIN-EVENT-010` as unused after
the corrected Windows root-lifecycle implementation removed the corresponding
platform-specific recursive output.

Evidence: `.github/policy/recursive-platform-exceptions.json`; pull request
16; policy run `30753558597`; stock-test run `30753558598`; recursive backend
integration run `30753558574`.

Decision: Remove the obsolete exception. The common recursive output now
applies on Windows, so retaining the entry would falsely document a native
event difference that recursive watching no longer exposes.

Fix commit: `7a5ab1ae081a4f28af1d5560b8ff53a223064035`

Validation runs: corrected candidate `policy`, `stock-test-gate`, and
`recursive-backend-integration` required.

### AUDIT-CI-002 | MAJOR | RESOLVED

ID: `AUDIT-CI-002`

Severity: `MAJOR`

Status: `RESOLVED`

Contract: `RC-023`, `RC-027`

Backend: all matrix-backed groups

Finding: The initial recursive backend workflow used GitHub Actions' default
matrix fail-fast behavior. A failed job therefore cancelled sibling Linux and
macOS jobs, preventing one submitted matrix from producing complete evidence.

Evidence: `.github/workflows/recursive-backend-integration.yml`; pull request
16; recursive backend integration run `30753558574`.

Decision: Set `strategy.fail-fast: false` on every matrix-backed job. Workflow
concurrency continues to use `cancel-in-progress: false`; neither setting
retries a failed test.

Fix commit: `7a5ab1ae081a4f28af1d5560b8ff53a223064035`

Validation runs: corrected candidate `recursive-backend-integration` required.

### AUDIT-FEN-002 | BLOCKER | RESOLVED

ID: `AUDIT-FEN-002`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-013`, `RC-023`

Backend: FEN

Finding: After renaming a directory within a recursively watched tree, FEN can
lose recursive coverage. One of ten identical contract repetitions could not
observe any event from 500 sentinel writes below the renamed directory.

Evidence: `backend_fen.go`; `recursive_contract_test.go`; pull request 16;
recursive backend integration run `30753558574`, illumos job `91511737837`.

Decision: Do not weaken, skip, or allowlist the common contract. Pull request
17 closes the FEN one-shot observation gap by re-associating non-terminal
events before dispatch or directory scanning and by associating newly found
recursive paths before their public Create events.

Fix commits: `fdffd75c88ff94dbf966de61f7c2ae742124ee0f`,
`a73009a30890d362b3470900d1d7b9ffe7f6170c`

Validation runs: focused FEN stress `30756792254`; complete candidates
`30757372940` and `30757583507`; post-merge stock test `30795667697`;
post-merge staticcheck `30795667675`.

### AUDIT-FEN-003 | BLOCKER | RESOLVED

ID: `AUDIT-FEN-003`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-003`, `RC-023`

Backend: FEN

Finding: A directory that existed when a recursive root was added could fail
to report a subsequently created sentinel file. One of ten identical contract
repetitions timed out while checking the pre-existing descendant set.

Evidence: `backend_fen.go`; `recursive_contract_test.go`; pull request 16;
recursive backend integration run `30753558574`, illumos job `91511737837`.
The same missing existing-descendant event recurred after the Fencing merge in
recursive backend integration run `30802982672`, illumos job `91651702258`,
while the other nine identical repetitions passed.

Decision: Preserve the platform-identical contract. The pull request 17 rearm
fix and the unconditional post-rearm reconciliation are necessary but not
sufficient. Event handling can still observe or mutate partially completed
Add and Remove transactions because it does not participate in their
lifecycle lock. Resolve this state race under `AUDIT-FEN-005` without changing
the common contract or its deadline.

Fix commits: `fdffd75c88ff94dbf966de61f7c2ae742124ee0f`,
`a73009a30890d362b3470900d1d7b9ffe7f6170c`,
`567baecb440fecf8d886002af9636aba0f826f18`,
`8696f5503312c1cfd0ab92ee065b2b308c9fa1f3`

Validation runs: previous evidence is focused FEN stress `30756792254`;
complete candidates `30757372940` and `30757583507`; post-merge stock test
`30795667697`; post-merge staticcheck `30795667675`. Corrective candidate run
`30805162440`, illumos job `91658718150`, still loses existing-descendant and
retained-owner observations. The complete lifecycle correction is statically
clean and still requires corrective GitHub Actions validation.

### AUDIT-FEN-004 | BLOCKER | RESOLVED

ID: `AUDIT-FEN-004`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-019`, `RC-020`, `RC-023`

Backend: FEN

Finding: Concurrent Close can make `EventPort.Get` return `this EventPort is
already closed` after the underlying port operation succeeds but before the
x/sys wrapper converts the retrieved event. The read loop exposes that
close-induced implementation error through `Watcher.Errors`.

Evidence: `backend_fen.go`; `recursive_contract_test.go`; pull request 16;
recursive backend integration run `30753558574`, illumos job `91511737837`.

Decision: Treat every EventPort retrieval error observed after the watcher is
closed as normal shutdown, while continuing to report retrieval errors from an
open watcher. Pull request 17 added a focused concurrent Close regression with
an active Errors consumer.

Fix commit: `dfa36f1e6fadb8d6a9a80414ebd0afe8d5cdc2aa`

Validation runs: focused FEN stress `30756792254`; complete candidates
`30757372940` and `30757583507`; post-merge stock test `30795667697`;
post-merge staticcheck `30795667675`.

### AUDIT-FEN-005 | BLOCKER | RESOLVED

ID: `AUDIT-FEN-005`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-003`, `RC-008`, `RC-009`, `RC-011`, `RC-019`, `RC-020`,
`RC-023`, `RC-026`, `RC-027`

Backend: FEN

Finding: FEN serializes public Add and Remove transactions with `opsMu`, but
event handling and Close mutate the same EventPort associations and ownership
state outside that transaction. A queued event can therefore reconcile
partially changed ownership, and Close can invalidate the EventPort while an
Add or event rearm is calling `AssociatePath`.

Evidence: `backend_fen.go`; commit
`e09f88c700c70d5b062ec94760b85fc6ddd2ff44`; recursive backend integration
run `30805162440`, illumos job `91658718150`. Ten unchanged contract
repetitions produced missing coverage for existing descendants and retained
owners, a stale prefix-similar renamed root, and two
`port.AssociatePath(...): bad file number` errors during concurrent lifecycle
operations. The surrounding SSH session completed normally and only reported
the inner `go test` exit status.

Decision: Treat Add, Remove, one complete event-state transaction, and the
EventPort portion of Close as one serialized FEN lifecycle. Event and error
delivery is not part of that critical section; it must occur in unchanged
order after state mutation releases the lifecycle lock. Close must publish the
shared shutdown signal before waiting for the lock, close the EventPort under
that lock, and wait for the read loop to close both public channels. Queued
events that reach the handler after shutdown must not mutate state.

Fix commits: `8696f5503312c1cfd0ab92ee065b2b308c9fa1f3`,
`d8e7818ecc36f57cee5b9220134c04aa91f8019e`

Validation runs: implementation and backend regression are statically clean;
corrective run `30806727820`, illumos job `91663711218`, exposed the
lock-held delivery deadlock recorded as `AUDIT-FEN-006`. The follow-up
lock-free publication candidate is statically clean and still requires
complete GitHub Actions validation.

### AUDIT-FEN-006 | BLOCKER | RESOLVED

ID: `AUDIT-FEN-006`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-019`, `RC-021`, `RC-023`, `RC-027`

Backend: FEN

Finding: The first `AUDIT-FEN-005` candidate held `opsMu` while publishing
events through the default unbuffered `Events` channel. A caller that receives
one event and then invokes Add or Remove can wait for `opsMu` while the FEN
handler waits for that same caller to receive another event.

Evidence: `backend_fen.go`; recursive backend integration run `30806727820`,
illumos job `91663711218`. The unchanged
`overlapping_roots_are_independent` contract test timed out after ten minutes:
the FEN read goroutine was blocked in `shared.sendEvent` from
`updateDirectory`, while the test goroutine was blocked acquiring `opsMu` in
`AddWith`.

Decision: Complete the native association and ownership transaction under
`opsMu`, queue its public events and errors in production order, release
`opsMu`, and only then publish to the public channels. Do not add buffering,
goroutines, sleeps, retries, or contract exceptions.

Fix commit: `d8e7818ecc36f57cee5b9220134c04aa91f8019e`

Validation runs: the deterministic FEN regression, formatting, and policy
checks are statically clean; complete corrective GitHub Actions validation is
pending.

### AUDIT-FEN-001 | BLOCKER | RESOLVED

ID: `AUDIT-FEN-001`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-017`, `RC-018`

Backend: FEN

Finding: A failed recursive Add could temporarily upgrade a directory already
tracked by a non-recursive parent and leave recursive scan state behind while
retaining the pre-existing EventPort association.

Evidence: `backend_fen.go`; `backend_fen_test.go`; pull request 15.

Decision: Recompute scan ownership when releasing the failed owner and compare
all internal maps and EventPort associations with their exact pre-call state.

Fix commit: `a28a6d92ec421dfd9a5b09a1206c81253eed702c`

Validation runs: pull-request test run `30564186435`; post-merge test run
`30752070285`.

### AUDIT-KQUEUE-001 | BLOCKER | RESOLVED

ID: `AUDIT-KQUEUE-001`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-018`, `RC-019`, `RC-020`, `RC-025`

Backend: kqueue

Finding: Public Add and Remove operations could race Close while descriptors
and the kqueue were being torn down, exposing raw `EBADF` and allowing a watch
to be registered after Close's cleanup snapshot.

Evidence: `backend_kqueue.go`; `backend_kqueue_test.go`; pull request 15.

Decision: Serialize public Add, Remove, and Close lifecycle operations while
preserving the existing backend event and ownership mechanisms.

Fix commit: `19200f7324cdead2027cd329a39cf94aeddc4179`

Validation runs: pull-request test run `30564186435`; post-merge test run
`30752070285`.

### AUDIT-KQUEUE-002 | BLOCKER | RESOLVED

ID: `AUDIT-KQUEUE-002`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-019`, `RC-020`, `RC-023`, `RC-027`

Backend: kqueue

Finding: `kqueue.Close` returns after closing the wakeup pipe but before the
read goroutine has closed the public `Events` and `Errors` channels. The stock
close test consequently observed an open `Events` channel after its existing
50 millisecond allowance on macOS 15 Intel with Go 1.23.

Evidence: `backend_kqueue.go`; `fsnotify_test.go`; post-merge stock test run
`30802982900`, job `91651702827`. The pull-request head
`8cdffe964c1226fb1517cb3b529686268be6b31b` and merge commit
`a9b0c5b675aa16c048f450a815fed8d51e882579` have the identical tree
`ed94d3cd9d74845be3a11fbf7dbd511dee0ac6a6`.

Decision: Do not increase the stock-test delay. Give kqueue an explicit read
loop completion signal and make every `Close` caller wait until descriptor
cleanup and both public channels have reached their terminal state.

Fix commit: `c06726c69ce16712ff312b15a8c10bc916bb47a1`

Validation runs: the deterministic kqueue close regression and complete
corrective candidate still require GitHub Actions validation.

### AUDIT-CI-003 | MAJOR | RESOLVED

ID: `AUDIT-CI-003`

Severity: `MAJOR`

Status: `RESOLVED`

Contract: `RC-023`, `RC-027`

Backend: all

Finding: Before the initial contract tag exists, the policy checker permits
only `d323a68b31cf4a5043d76f95680777b5fa5f6696` as its event base. After the
Fencing merge at `a9b0c5b675aa16c048f450a815fed8d51e882579`, every corrective
production pull request would therefore fail policy before its code can be
validated.

Evidence: `.github/scripts/check-recursive-policy.sh`; post-merge runs
`30802982672` and `30802982900`.

Decision: Keep the historical Fencing comparison base, but permit event bases
that descend from the exact merged Fencing candidate until a valid contract
tag exists. Continue comparing each corrective candidate with its actual pull
request or push base; do not permit unrelated histories or bypass any file,
trailer, dependency, formatting, or audit checks.

Fix commit: `b58fabe0e091f3af2bfff09b5f570285b930420d`

Validation runs: candidate `policy` and post-merge `policy` still require
GitHub Actions validation.

### AUDIT-WIN-001 | BLOCKER | RESOLVED

ID: `AUDIT-WIN-001`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-006`, `RC-007`, `RC-012`, `RC-017`, `RC-018`, `RC-023`,
`RC-026`

Backend: Windows IOCP

Finding: Renaming a recursive root could leave the old explicit root in
`WatchList` because `ReadDirectoryChangesW` did not reliably report the root's
own rename through the recursive root handle.

Evidence: `backend_windows.go`; `backend_windows_test.go`; pull request 15.

Decision: Add a hidden parent monitor for recursive roots, keep it out of
`WatchList`, and remove stale root state without affecting prefix-similar roots.

Fix commit: `afd00338e6d9c8331633990e326313acc18e7553`

Validation runs: pull-request test run `30564186435`; post-merge test run
`30752070285`.

### AUDIT-CI-004 | MAJOR | RESOLVED

ID: `AUDIT-CI-004`

Severity: `MAJOR`

Status: `RESOLVED`

Contract: `RC-027`

Backend: all statically analyzed targets

Finding: The required Staticcheck workflow reported success while its cache
maintenance emitted `Process completed with exit code 1`. The inherited
workflow attempted to delete a mutable cache through a token without cache
deletion permission, ignored that failure with `continue-on-error`, then
attempted to save the same occupied key. It also restored overlapping Go build
and Staticcheck caches and used Node 20 action versions that GitHub now runs
through a deprecated compatibility path.

Evidence: `.github/workflows/staticcheck.yml`; post-merge Staticcheck run
`30810808832`, job `91676823970`.

Decision: Preserve the existing all-target `go vet` and Staticcheck failure
semantics, but remove mutable cache deletion, duplicate cache layers, and every
ignored cache failure. Use the cache built into `actions/setup-go`, one
SHA-qualified immutable Staticcheck cache with restore prefixes, Node 24 action
majors, and the released Staticcheck version `2026.1` instead of `@latest`.

Fix commit: `ci: make staticcheck gate deterministic`

Validation runs: candidate Staticcheck and policy runs required; complete
pull-request checks remain required before merge.

### AUDIT-CI-005 | MINOR | RESOLVED

ID: `AUDIT-CI-005`

Severity: `MINOR`

Status: `RESOLVED`

Contract: `RC-027`

Backend: all matrix-backed groups

Finding: Stock matrix jobs inherited a mixture of job IDs and generic `test`
labels, while the recursive workflow used another inconsistent set of backend
labels. The Actions UI consequently obscured which evidence belonged to the
upstream-compatible stock suite and which belonged to the exhaustive recursive
suite.

Evidence: `.github/workflows/test.yml`;
`.github/workflows/recursive-backend-integration.yml`; pull request 20.

Decision: Give both workflows and every backend job an explicit `Stock / ...`
or `Recursive / ...` display name. Preserve all job IDs, matrices, commands,
dependencies, and the required aggregator names `stock-test-gate`,
`recursive-contract`, `resource-invariants`, and
`recursive-backend-integration`.

Fix commit: `ci: clarify backend matrix names`

Validation runs: updated pull-request policy, stock, Staticcheck, and recursive
backend workflows required.

### AUDIT-CI-006 | MINOR | RESOLVED

ID: `AUDIT-CI-006`

Severity: `MINOR`

Status: `RESOLVED`

Contract: `RC-027`

Backend: BSD/kqueue and illumos/FEN workflow presentation

Finding: Explicit display names made individual jobs identifiable, but the
Actions graph still grouped the four standalone BSD jobs and standalone
illumos job into one shared tile. Unlike Linux, Windows, and macOS, the two
backend families therefore had no distinct matrix tiles.

Evidence: `.github/workflows/test.yml`;
`.github/workflows/recursive-backend-integration.yml`; post-merge runs
`30818715321` and `30818715188`.

Decision: Represent BSD as one four-entry matrix and illumos as its own
one-entry matrix in both workflows. Preserve every runner, VM action, setup
command, test command, timeout, and required aggregator name. Change only the
job topology and the corresponding aggregator dependencies.

Fix commit: `ci: group backend jobs by platform family`

Validation runs: updated pull-request policy, stock, Staticcheck, and recursive
backend workflows required.

### AUDIT-CI-007 | MAJOR | RESOLVED

ID: `AUDIT-CI-007`

Severity: `MAJOR`

Status: `RESOLVED`

Contract: `RC-027`

Backend: audit governance

Finding: The policy rejected every open or in-progress BLOCKER and MAJOR
finding globally. This made the required audit-first workflow impossible:
recording a genuine finding immediately blocked every candidate, including an
independent fix for that finding.

Evidence: `.github/scripts/check-recursive-policy.sh` before this correction;
the quality plan requires findings to exist before production changes begin.

Decision: Permit unresolved findings during normal incremental audit work. A
production change must reference a finding that uniquely existed as `OPEN` or
`IN_PROGRESS` in the event base and must leave it `RESOLVED` in the candidate.
Unrelated open findings do not block independent work. An explicit
`AUDIT_REQUIRE_COMPLETE=1` mode remains available for final release gates and
rejects every unresolved BLOCKER or MAJOR finding.

Fix commit: `ci: allow incremental recursive audit findings`

Validation runs: policy self-tests cover open-ledger, valid transition,
unresolved, same-candidate, already-resolved, independent-finding, and final
completion cases; one complete pull-request run is required.

## Platform Exceptions

Every platform exception must be represented in
`.github/policy/recursive-platform-exceptions.json` and must reference a
matching `AUDIT-TEST-*` finding.

No exception is accepted merely because a path or test file exists. The
non-recursive evidence must execute on the affected platform and assert the
claimed native behavior.

## Benchmarks And Resources

No performance or resource baseline has been established yet. This work starts
only after the contract freeze, as required by the quality plan.

## Decisions

- The Recursive Control Contract is platform-identical.
- Native event, capability, and test-environment differences remain separate
  from recursive control semantics.
- Contract tests are not changed to accommodate production behavior.
- Builds and platform tests run only on GitHub-hosted runners.
- The user retains manual control over pull-request publication and merge.
