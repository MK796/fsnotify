# Recursive Watch Working Rules

These instructions are binding for every agent working in `MK796/fsnotify`.

## Required Reading

Before changing recursive-watch documentation, tests, CI, or production code,
read all of:

- `RECURSIVE_WATCH_QUALITY_PLAN.md`
- `RECURSIVE_WATCH_FENCING_PLAN.md`
- `RECURSIVE_WATCH_CONTRACT.md`
- `QUALITY_AUDIT.md`

The repository files and CI gates take precedence over chat memory.

## Scope

- Work exclusively on fsnotify.
- Do not add Traefik-specific or other downstream-specific code, tests,
  workflows, or documentation.
- Do not add a runtime dependency.
- Do not add an exported API symbol without a separately approved contract
  change.

## Recursive Control Contract

- The Recursive Control Contract is identical on inotify, IOCP, kqueue, and
  FEN.
- A backend may differ only in native event representation, native capability,
  or test-environment capability already proven outside recursive watching.
- A Recursive Control Contract difference is always a defect. It cannot be
  accepted or allowlisted.
- Do not add production event normalization to hide backend differences.

## Change Control

- Before the initial contract freeze, do not change production backend files or
  begin the production-code audit.
- After the initial contract freeze, do not change production code without an
  `AUDIT-*` finding and affected `RC-*` rule.
- Every production commit must contain `Audit:` and `Contract:` trailers.
- Keep each production or refactoring commit limited to one responsibility.
- Do not mix production changes with contract, common contract-test, allowlist,
  policy-metatest, or fencing-rule changes.
- During production work, frozen contract files and central contract semantics
  must remain unchanged.
- Do not weaken tests, loosen expectations, add unconditional sleeps, increase
  waits to hide failures, or add blind retries.
- Treat every new platform exception as a blocker until a non-recursive test
  proves and documents its classification.
- A failed contract test must be corrected in production code. A contract
  correction follows the separate contract-change process.

## Contract Approval

- The agent must not infer, fabricate, record, or commit contract approval on
  behalf of the user.
- A contract change requires explicit user approval outside the candidate
  commit.
- A contract change must be isolated in its own pull request or commit, contain
  no production code, identify affected `RC-*` rules, and receive a new
  monotonically versioned contract tag.
- Production code implementing a contract change must be committed separately
  after the contract change is approved.

## Execution

- Run builds, platform tests, stress tests, and benchmarks only on
  GitHub-hosted runners, never on deployment hosts.
- Keep at most one temporary remote working branch.
- Keep at most one full-matrix run active.
- Use global workflow concurrency with `cancel-in-progress: false` for the full
  recursive backend matrix.
- Stop after a genuine test failure and establish its cause before starting
  another run.
- Do not auto-resolve upstream conflicts.
- Do not automatically publish or merge a pull request.
- The final merge decision belongs to the user.
- A source change or genuine test failure resets the final three-run matrix.

## Frozen Contract

After a valid `recursive-watch-contract-v1` tag is created on a green `main`
commit, production work must not change:

- `RECURSIVE_WATCH_CONTRACT.md`
- the common recursive contract suite
- `.github/policy/recursive-platform-exceptions.json`
- the platform-exception policy metatest
- central contract-test semantics
- fencing-relevant rules in this file

The initial tag must not be created on an unmerged working branch.

## Audit And Completion

- `QUALITY_AUDIT.md` is the only progress and findings ledger.
- Recursive-control differences have no accepted-exception status.
- The work is incomplete while any `BLOCKER` or `MAJOR` is open, any native
  exception lacks executable non-recursive evidence, any required CI check is
  red, or the final matrix remains flaky.
