# Recursive Watch Quality Audit

This file is the sole progress and findings ledger for recursive-watch quality
work in this fork.

## Reference

```text
Repository: MK796/fsnotify
Upstream base: 20b1e15
Historical validated implementation: fce4bab
Historical reference tag: recursive-watch-validation-v1
Corrected fencing base: b6247e0b3a207183cf506ecb27e8f71fe985f3b2
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

No production-audit finding is open. Production-code auditing has not started.

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

Decision: Pin the initial Fencing comparison to `b6247e0b`, use the documented
`Audit:` and `Contract:` trailers, freeze the agent rules, reject new
unconditional test sleeps, and set full-matrix cancellation to false.

Fix commit: `ci: correct recursive watch governance gates`

Validation runs: candidate `policy`, `api-compatibility`, and
`recursive-backend-integration` required.

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
