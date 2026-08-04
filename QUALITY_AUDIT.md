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
Current audit baseline: 21e7f809b95c9b955b6d3b2427672155b063e84d
Working branch: quality/backend-audit-v1
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

Validation runs: pull request 24 policy and API compatibility `30830891032`;
recursive backend matrix `30830891346`; Staticcheck `30830891584`; stock
backend matrix `30830887742`. Post-merge stock `30832742391`, recursive
`30832742383`, and Staticcheck `30832742146` passed.

### AUDIT-COMMON-002 | MINOR | RESOLVED

ID: `AUDIT-COMMON-002`

Severity: `MINOR`

Status: `RESOLVED`

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

Fix commit: `refactor: remove obsolete recursive feature gate`

Validation runs: pull request 24 policy and API compatibility `30830891032`;
recursive backend matrix `30830891346`; Staticcheck `30830891584`; stock
backend matrix `30830887742`. Post-merge stock `30832742391`, recursive
`30832742383`, and Staticcheck `30832742146` passed.

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

Evidence: `.github/scripts/check-recursive-policy.sh` before the original
correction; the quality plan requires findings to exist before production
changes begin. The policy step in pull request 26 accepted four newly recorded
open blockers, but `test-recursive-policy.sh` then cloned that real candidate
ledger into every fixture. Its completion-mode success case consequently
failed on the unrelated real findings instead of testing its own synthetic
ledger transition.

Decision: Permit unresolved findings during normal incremental audit work. A
production change must reference a finding that uniquely existed as `OPEN` or
`IN_PROGRESS` in the event base and must leave it `RESOLVED` in the candidate.
Unrelated open findings do not block independent work. An explicit
`AUDIT_REQUIRE_COMPLETE=1` mode remains available for final release gates and
rejects every unresolved BLOCKER or MAJOR finding. Every self-test case must
start from a synthetic clean fixture ledger so current repository findings
cannot alter the expected result.

Fix commits: `ci: allow incremental recursive audit findings`;
`test: isolate policy audit fixtures`

Validation runs: policy self-tests cover source-ledger isolation, open-ledger,
valid transition, unresolved, same-candidate, already-resolved,
independent-finding, and final completion cases; one complete pull-request run
is required.

### AUDIT-CI-008 | MAJOR | RESOLVED

ID: `AUDIT-CI-008`

Severity: `MAJOR`

Status: `RESOLVED`

Contract: `RC-027`

Backend: audit governance

Finding: The policy applies its per-production-commit `Audit:` and `Contract:`
trailer requirement to both pull-request candidates and post-merge `main`
pushes. GitHub squash merging replaces the individually validated source
commits with one new commit whose default message does not preserve those
trailers. A valid, fully green production pull request therefore produces a
false policy failure after merge even though its tree and audit transitions
are unchanged.

Evidence: pull request 24 policy run `30830891032` passed both source commits;
squash commit `fd0580d42cc10219622176c639710e52a369d78d`; post-merge policy run
`30832742346` failed only because that generated squash commit lacks trailers.
The same post-merge tree passed stock run `30832742391`, recursive run
`30832742383`, API compatibility in `30832742346`, and Staticcheck run
`30832742146`.

Decision: Keep per-commit `Audit:` and `Contract:` enforcement unchanged for
pull-request candidates. For post-merge push validation, derive accountability
from the event-base and candidate ledgers: every candidate containing a
production change must contain at least one finding that uniquely transitions
from `OPEN` or `IN_PROGRESS` to `RESOLVED`. Continue enforcing frozen-file
separation, dependency, formatting, test-sleep, and all tree-level governance
checks in both modes. Add isolated policy self-tests for a valid squash,
production without an actionable transition, and a finding created and
resolved only in the squash candidate. Branch protection remains responsible
for ensuring production reaches `main` through a validated pull request.

Fix commit: `ci: support squash-merge policy validation`

Validation runs: isolated pull-request and push policy self-tests plus one
complete candidate run required.

## Backend Audit Coverage

The backend audit compared baseline `bcf85a1` with upstream base `20b1e15`,
the frozen contract, the common public contract suite, backend-native tests,
and the platform-exception policy. The review covered every changed recursive
production path in:

- `backend_inotify.go`: ownership maps, initial and dynamic tree
  registration, rollback, descriptor removal, pending move expiry, subtree
  detach and rebase, event filtering, `WatchList`, and `Close`.
- `backend_windows.go`: IOCP input serialization, physical watch creation,
  hidden root-parent monitors, provisional state, cancellation and completion
  ownership, root cleanup, rename handling, `WatchList`, and `Close`.
- `backend_kqueue.go`: descriptor and path maps, explicit and recursive
  ownership, initial and dynamic tree registration, rollback, rescan and
  rename reconciliation, descriptor removal, `WatchList`, and `Close`.
- `backend_fen.go`: one-shot association and rearm, explicit and recursive
  ownership, initial and dynamic tree registration, rollback, rename cache,
  directory reconciliation, dissociation, `WatchList`, and `Close`.

The common contract tests are correctly kept in the public external test
package and contain no backend type assertions, platform branches, or skips.
Backend tests are limited to native state, resource, and lifecycle invariants.
Raw event differences remain isolated in script tests and the machine-checked
allowlist. The one material placement gap is recorded as `AUDIT-TEST-006`:
dynamic registration rollback is a common control requirement but is tested
only for inotify.

The following matrix accounts for every frozen rule and identifies the
production path that implements it. `native subtree` means that Windows uses
one recursive `ReadDirectoryChangesW` operation rather than one physical watch
per descendant.

| Contract | inotify | Windows/IOCP | kqueue | FEN |
|---|---|---|---|---|
| `RC-001` | `AddWith`, `register` | `AddWith`, `addWatch` | `AddWith`, `addWatch` | `AddWith`, `addRecursive` |
| `RC-002` | `AddWith` rollback | validation before `addWatch` commit | `AddWith` rollback | `addRecursive`, `releaseOwner` |
| `RC-003` | initial `WalkDir` | native subtree | initial `WalkDir` | `addRecursive` |
| `RC-004` | `registerRecursiveSubtree` | native subtree | `sendCreateIfNew`, `addRecursiveSubdir` | `updateDirectory`, `addRecursiveSubdir` |
| `RC-005` | `byUser` idempotence | existing inode/mask handling | `hasUserWatch` | `userWatch`, refresh rearm |
| `RC-006` | `byUser`, `WatchList` | masks plus hidden monitor bit | `byUser`, `WatchList` | `byUser`, `WatchList` |
| `RC-007` | internal owners excluded | hidden parent monitor excluded | internal paths excluded | owners excluded |
| `RC-008` | owner sets | masks and root relations | owner sets | owner sets |
| `RC-009` | owner sets | independent `roots` entries | owner sets | owner sets |
| `RC-010` | `recursivePath`, `releaseOwner` | `recursivePath`, `remWatch` | `recursivePath`, `Remove` | `recursivePath`, `Remove` |
| `RC-011` | `releaseOwner` | per-root relation removal | `releaseOwner` | `releaseOwner` |
| `RC-012` | `handleEvent`, `remove` | hidden parent monitor cleanup | `removePhysical`, `removeRootsUnder` | `dropPhysical` |
| `RC-013` | pending move plus `rebase` | native subtree path continuity | identity match plus `rebase` | rename cache plus rescan |
| `RC-014` | pending move expiry, `detachSubtree` | native subtree boundary | `removePhysical` | `dropPhysical` |
| `RC-015` | `registerRecursiveSubtree` | native subtree | `addRecursiveSubdir` | `addRecursiveSubdir`, `addRenamedSubdir` |
| `RC-016` | `detachPendingMoveAt` | per-completion operation identity | `seen` plus file identity | `info`, `tracksDirectory` |
| `RC-017` | registration ledger rollback | provisional rollback | initial rollback only | initial rollback only |
| `RC-018` | owner release plus descriptor removal | completion drain plus handle close | owner release plus descriptor close | owner release plus dissociation |
| `RC-019` | shared mutex | IO thread plus state mutex | `opsMu`, `watchMu`, state mutex | `opsMu`, state mutex |
| `RC-020` | shared close plus `doneResp` | `markClosed`, `closeDone` | shared close plus `doneResp` | shared close plus `doneResp` |
| `RC-021` | close signal releases channel sends | closed `done` releases channel sends | shared close before wait | notifications sent outside `opsMu` |
| `RC-022` | owner-filtered native events | mask-filtered native events | rescan synthesis only | queued native notifications only |
| `RC-023` | common suite | common suite | common suite | common suite |
| `RC-024` | native script evidence | allowlisted native script evidence | native script evidence | native script evidence |
| `RC-025` | `releaseOwner`, closed checks | input replies, closed checks | `Remove`, closed checks | `Remove`, closed checks |
| `RC-026` | `hasPathPrefix` | `hasWindowsPathPrefix` | `hasPathPrefix` | `hasPathPrefix` |
| `RC-027` | non-recursive owner path | non-recursive mask path | non-recursive owner path | non-recursive owner path |

This accounting found no new inotify control or lifecycle defect. Its dynamic
registration path records every newly added owner and removes those
registrations on failure. Full-map scans in inotify and kqueue remain explicit
performance-baseline subjects; without measurements they are not classified
as defects. The currently unreachable internal `sendCreate` option is likewise
not a runtime finding and belongs to the later simplification pass.

### AUDIT-TEST-006 | BLOCKER | OPEN

ID: `AUDIT-TEST-006`

Severity: `BLOCKER`

Status: `OPEN`

Contract: `RC-004`, `RC-015`, `RC-017`, `RC-018`, `RC-023`

Backend: all registration architectures

Finding: The frozen contract requires atomic rollback after partial recursive
registration, but the shared and backend suites prove this only for the
initial `Add` call, except for one inotify-only dynamic subtree test. No common
scenario forces registration of a newly created or moved-in populated subtree
to fail after at least one descendant has already been registered. The suite
therefore did not detect that kqueue and FEN retain partial dynamic state, and
it does not prove that Windows has completed asynchronous rollback before
`Add` returns after hidden root-monitor setup fails.

Evidence: `TestRecursiveContract/reject_invalid_roots_atomically`;
`TestRecursiveContractResourceInvariants`; backend initial-Add rollback tests;
`TestInotifyRecursiveSubtreeRegistrationRollback`; absence of equivalent
dynamic rollback coverage for IOCP, kqueue, and FEN; `AUDIT-WIN-002`,
`AUDIT-KQUEUE-003`, and `AUDIT-FEN-007`.

Decision: Add deterministic backend-native failure injection for each native
resource operation and apply the same pre-call/post-failure state assertions in
each backend test. Keep the public contract suite backend-independent; do not
expose a failure hook through the library API. Backend tests must additionally
inspect pending IOCP operations, descriptors, EventPort associations, owners,
and internal maps as applicable. Do not use permissions, timing races, sleeps,
or retries as failure injection.

Fix commit:

Validation runs:

### AUDIT-WIN-002 | BLOCKER | OPEN

ID: `AUDIT-WIN-002`

Severity: `BLOCKER`

Status: `OPEN`

Contract: `RC-002`, `RC-017`, `RC-018`, `RC-023`

Backend: Windows/IOCP

Finding: A newly created recursive root starts an asynchronous
`ReadDirectoryChangesW` operation before its hidden parent monitor is fully
validated. If parent-monitor creation or root identity validation then fails,
`addWatch` restores the logical mask and calls `startRead` to cancel the active
operation. Cancellation is asynchronous: `startRead` returns immediately while
the operation remains in `pending`, the watch remains in `watches`, and its
handle remains open until the I/O thread dequeues the aborted completion. The
buffered input reply can therefore make public `Add` return before the exact
pre-call internal and native resource state has been restored.

Evidence: `backend_windows.go`: `addWatch`, `addRecursiveRootMonitor`,
`rollbackRecursiveRootMonitor`, `startRead`, and the IOCP completion path in
`readEvents`. `TestWindowsRecursiveAddRollback` rejects invalid roots before
this asynchronous rollback path and does not inspect it.

Decision: Make failed recursive Add a transaction owned by the I/O thread.
Delay its reply until every operation created solely by that Add has completed
cancellation and its watch, handle, root relation, and pending entry have been
removed. Add deterministic failure injection after the root read starts and
assert the exact pre-call logical and native resource state before `Add`
returns.

Fix commit:

Validation runs:

### AUDIT-KQUEUE-003 | BLOCKER | OPEN

ID: `AUDIT-KQUEUE-003`

Severity: `BLOCKER`

Status: `OPEN`

Contract: `RC-004`, `RC-015`, `RC-017`, `RC-018`, `RC-023`

Backend: kqueue

Finding: `addRecursiveSubdir` registers and assigns owners to descendants as
`WalkDir` progresses, but on a later `WalkDir`, `Info`, open, or kevent
registration error it returns without releasing any registrations already
created by that invocation. The caller reports the error while partial path,
descriptor, owner, directory, and seen state remains live. Initial recursive
`Add` has a rollback path, so the behavior differs specifically for dynamic
created or moved-in subtrees.

Evidence: `backend_kqueue.go`: `sendCreateIfNew`, `addRecursiveSubdir`,
`internalWatch`, and `addWatch`; contrast with rollback in `AddWith` and with
the registration ledger in inotify's `registerRecursiveSubtree`.
`TestKqueueRecursiveAddRollback` covers only initial Add.

Decision: Record every owner and physical resource acquired by one dynamic
subtree registration. On failure, unwind only those acquisitions in reverse
order while preserving pre-existing and overlapping owners. Return joined
registration and cleanup errors. Add deterministic mid-tree failure coverage
for logical maps and open descriptors.

Fix commit:

Validation runs:

### AUDIT-FEN-007 | BLOCKER | OPEN

ID: `AUDIT-FEN-007`

Severity: `BLOCKER`

Status: `OPEN`

Contract: `RC-004`, `RC-015`, `RC-017`, `RC-018`, `RC-023`

Backend: FEN

Finding: `addRecursiveSubdir` and `addRenamedSubdir` associate descendants and
assign owners incrementally during `WalkDir`. If a later entry fails, both
functions return without undoing associations, ownership, directory scan state,
or remembered identity created earlier in that invocation. `updateDirectory`
queues the error and continues, leaving a partially registered dynamic subtree.
Initial recursive `Add` calls `releaseOwner` on failure, so the dynamic path has
different rollback semantics.

Evidence: `backend_fen.go`: `updateDirectory`, `addRecursiveSubdir`,
`addRenamedSubdir`, `associateOwned`, and `associateFileLocked`; contrast with
`addRecursive` rollback in `AddWith` and with inotify's registration ledger.
`TestFenRecursiveAddRollback` covers only initial Add.

Decision: Track all association and ownership mutations made by one dynamic
tree walk and restore their exact previous state in reverse order on failure.
Preserve pre-existing owners and associations, return joined registration and
dissociation errors, and add deterministic mid-tree failure coverage for
EventPort associations and every logical map.

Fix commit:

Validation runs:

### AUDIT-KQUEUE-004 | BLOCKER | RESOLVED

ID: `AUDIT-KQUEUE-004`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-004`, `RC-018`, `RC-023`, `RC-024`

Backend: kqueue on DragonFly BSD

Finding: The recursive `add-dir` script intermittently loses the second
`Remove` event when the same path is created, removed, recreated, removed, and
then created again. The failed run observed both `Create` events around the
second file lifetime but only one `Remove`. The identical production baseline
and script passed on the preceding `main` run and on the controlled rerun, so
this is an intermittent event-lifecycle defect rather than a policy-change
regression. The current evidence does not yet distinguish a recursive kqueue
descriptor/state race from a pre-existing native non-recursive limitation or
a script-harness synchronization defect.

Evidence: pull-request run `30850609674`, initial DragonFly BSD job
`91809431946`, `TestScript/watch-recurse/add-dir`; successful preceding
baseline job `91768647811`; successful controlled rerun job `91813398424`.
The failure occurred after ten successful common contract iterations and
inside the stock repository suite, before the dedicated repeated recursive
script command.

Decision: Compare the identity stored with a physical kqueue descriptor against
the object currently present at that pathname during a directory rescan. If the
identity changed, leave the old descriptor untouched until its already pending
native `NOTE_DELETE` or `NOTE_RENAME` is consumed. Do not rearm that stale
descriptor with `EV_CLEAR`, synthesize replacement events, or introduce a
platform exception. The deterministic backend regression forces this state for
both recursive and non-recursive directory watches, verifies that descriptor,
owner, and `seen` state remain intact, and reads the pending `NOTE_DELETE`
directly from kqueue without sleeps or retries.

Fix commit: `6357174952f2114a22426dd5bbd60194c343c57c`

Validation runs: pull request 29 corrected candidate recursive matrix
`30927882858`, stock matrix `30927883108`, policy `30927885778`, and
Staticcheck `30927882835` passed. The recursive matrix passed on every target,
including DragonFly BSD and OpenBSD. Post-merge recursive matrix `30930764665`,
stock matrix `30930767270`, policy `30930764713`, and Staticcheck
`30930764720` passed on squash commit
`dfd812dc021d8ea5c3623e57a6eead7ad73fe460`.

### AUDIT-TEST-007 | BLOCKER | RESOLVED

ID: `AUDIT-TEST-007`

Severity: `BLOCKER`

Status: `RESOLVED`

Contract: `RC-004`, `RC-023`

Backend: shared recursive script harness; observed on macOS/kqueue

Finding: `testdata/watch-recurse/mkdir-p-nested` assumes the fixed 50
millisecond separator after `mkdir -p` is a recursive-registration readiness
barrier. It is not. The backend may still be registering the newly discovered
deepest directory when the script immediately creates its file, so the file
event can be missed even though every directory event and all later repeated
recursive script runs succeed. The public contract suite already treats a
native directory event as insufficient proof of registration and uses bounded
behavioral probes instead.

Evidence: pull request 29, corrected candidate run `30927320709`, macOS ARM
with Go 1.23 job `92052882096`. The full repository suite missed only the
`CREATE /a/b/c/file` event; the same job subsequently passed all 20 dedicated
recursive script repetitions. The changed kqueue branch is reached only for an
existing path whose object identity changed, while this script creates every
path once.

Decision: Add an explicit `await-recurse` script action after `mkdir -p`. It
creates uniquely named probe files under the new subtree until the collector
observes an event for that reserved path prefix or the bounded deadline
expires. Event arrival wakes the action directly; the periodic unique probes
only handle writes made before registration completed. Before transcript
comparison, discard events for the reserved probe prefix and remove only the
`Write` bit which creating a probe can produce for its direct parent directory;
preserve combined operations and every required directory and user-file event.
Do not increase a sleep, add a blind command retry, relax output, add a
platform exception, or change a backend or the frozen contract.

Fix commit: `test: make recursive script readiness event-driven`

Validation runs: initial pull-request candidate `55d7076` passed policy, API
compatibility, Staticcheck, and all 17 recursive backend jobs. Both Windows
stock jobs exposed a test-internal parent-directory `Write` side effect from
the readiness probe; no backend or contract failure occurred. Complete
corrected pull-request policy, stock, recursive backend, and Staticcheck
workflows required.

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
