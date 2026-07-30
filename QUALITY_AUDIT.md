# Recursive Watch Quality Audit

This file is the sole progress and findings ledger for recursive-watch quality
work in this fork.

## Reference

```text
Repository: MK796/fsnotify
Upstream base: 20b1e15
Historical validated implementation: fce4bab
Historical reference tag: recursive-watch-validation-v1
Corrected fencing base: a7b03eef28b15bef1c1b88530b117572c3d19378
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

Decision: Pin the initial Fencing comparison to `a7b03eef`, use the documented
`Audit:` and `Contract:` trailers, freeze the agent rules, reject new
unconditional test sleeps, and set full-matrix cancellation to false.

Fix commit: `ci: correct recursive watch governance gates`

Validation runs: candidate `policy`, `api-compatibility`, and
`recursive-backend-integration` required.

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
