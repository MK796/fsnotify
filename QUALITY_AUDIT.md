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
Working branch: quality/recursive-watch-v1
Contract tag: not valid until created on a green main commit
```

Production-code auditing starts only after the fencing pull request is merged,
the contract is validly tagged on `main`, and branch protection is active.

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

The systematic production-code audit has not started. Three production
blockers discovered by the initial Fencing contract run were resolved through
pull request 17 and independently validated before the Fencing work was
rebased onto the corrected production base. The first post-merge Fencing runs
then exposed one remaining FEN observation gap and one asynchronous kqueue
shutdown gap; both have narrowly scoped corrective candidates below.

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
fix is insufficient: an EventPort association consumed by a non-terminal
directory event still has a one-shot interval before re-association. A change
inside that interval can be lost unless the directory is reconciled after the
association is restored, independent of the native event bit that consumed it.
Correct the FEN event path without changing the common contract or its
deadline.

Fix commits: `fdffd75c88ff94dbf966de61f7c2ae742124ee0f`,
`a73009a30890d362b3470900d1d7b9ffe7f6170c`,
`567baecb440fecf8d886002af9636aba0f826f18`

Validation runs: previous evidence is focused FEN stress `30756792254`;
complete candidates `30757372940` and `30757583507`; post-merge stock test
`30795667697`; post-merge staticcheck `30795667675`. The new deterministic
FEN regression and complete corrective candidate still require GitHub Actions
validation.

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
