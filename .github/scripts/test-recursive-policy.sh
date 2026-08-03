#!/bin/sh

set -eu

root=$(mktemp -d "${RUNNER_TEMP:-/tmp}/fsnotify-policy-test.XXXXXX")
trap 'rm -rf "$root"' EXIT HUP INT TERM
source_repo=$(pwd)
case_number=0

new_case() {
	case_number=$((case_number + 1))
	case_dir=$root/case-$case_number
	git clone -q --no-hardlinks "$source_repo" "$case_dir"
	cd "$case_dir"
	git config user.name "Recursive policy test"
	git config user.email "recursive-policy@example.invalid"
	normalize_fixture_ledger
}

normalize_fixture_ledger() {
	if ! grep -Eq '^### AUDIT-.* \| (BLOCKER|MAJOR|MINOR) \| (OPEN|IN_PROGRESS)$' QUALITY_AUDIT.md; then
		return
	fi

	# Each case tests only the findings it creates below. Repository findings
	# must not leak into a completion-mode fixture and change its outcome.
	sed -i 's/^### \(AUDIT-.* \| \(BLOCKER\|MAJOR\|MINOR\) \| \)\(OPEN\|IN_PROGRESS\)$/### \1RESOLVED/' QUALITY_AUDIT.md
	sed -i 's/^Status: `\(OPEN\|IN_PROGRESS\)`$/Status: `RESOLVED`/' QUALITY_AUDIT.md
	git add QUALITY_AUDIT.md
	git commit -q -m "test fixture: isolate audit ledger"
}

append_finding() {
	id=$1
	severity=$2
	status=$3
	cat >>QUALITY_AUDIT.md <<EOF

### $id | $severity | $status

ID: \`$id\`

Severity: \`$severity\`

Status: \`$status\`

Contract: \`RC-027\`

Backend: policy fixture

Finding: Policy transition fixture.

Evidence: Policy transition fixture.

Decision: Policy transition fixture.

Fix commit: Policy transition fixture.

Validation runs: Policy transition fixture.
EOF
}

resolve_finding() {
	id=$1
	sed -i "s/^### $id | \(BLOCKER\|MAJOR\|MINOR\) | \(OPEN\|IN_PROGRESS\)$/### $id | \\1 | RESOLVED/" QUALITY_AUDIT.md
	sed -i "/^ID: \`$id\`$/,/^Status:/ s/^Status: \`\(OPEN\|IN_PROGRESS\)\`$/Status: \`RESOLVED\`/" QUALITY_AUDIT.md
}

commit_docs() {
	git add QUALITY_AUDIT.md
	git commit -q -m "$1"
}

commit_production() {
	id=$1
	cat >audit_policy_fixture.go <<'EOF'
package fsnotify

const auditPolicyFixture = true
EOF
	git add QUALITY_AUDIT.md audit_policy_fixture.go
	git commit -q -m "test: exercise audit policy" \
		-m "Audit: $id" \
		-m "Contract: RC-027"
}

commit_production_without_trailers() {
	cat >audit_policy_fixture.go <<'EOF'
package fsnotify

const auditPolicyFixture = true
EOF
	git add QUALITY_AUDIT.md audit_policy_fixture.go
	git commit -q -m "test: exercise squashed production policy"
}

expect_pass() {
	base=$1
	shift
	if ! output=$(POLICY_BASE_SHA=$base "$@" sh .github/scripts/check-recursive-policy.sh 2>&1); then
		printf '%s\n' "$output" >&2
		echo "policy self-test: expected success" >&2
		exit 1
	fi
}

expect_fail() {
	base=$1
	expected=$2
	shift 2
	if output=$(POLICY_BASE_SHA=$base "$@" sh .github/scripts/check-recursive-policy.sh 2>&1); then
		echo "policy self-test: expected failure" >&2
		exit 1
	fi
	if ! printf '%s\n' "$output" | grep -Fq "$expected"; then
		printf '%s\n' "$output" >&2
		echo "policy self-test: missing expected diagnostic: $expected" >&2
		exit 1
	fi
}

# Completion fixtures are independent from unresolved findings in the source
# repository and from findings created by another case.
new_case
append_finding AUDIT-COMMON-899 BLOCKER OPEN
commit_docs "docs: seed unrelated source finding"
normalize_fixture_ledger
base=$(git rev-parse HEAD)
expect_pass "$base" env AUDIT_REQUIRE_COMPLETE=1

# An unrelated open blocker is legal during incremental audit work.
new_case
base=$(git rev-parse HEAD)
append_finding AUDIT-COMMON-900 BLOCKER OPEN
commit_docs "docs: record open blocker"
expect_pass "$base" env AUDIT_REQUIRE_COMPLETE=0

# A production change may resolve a finding that was actionable in its base.
new_case
append_finding AUDIT-COMMON-901 MAJOR OPEN
commit_docs "docs: record production finding"
base=$(git rev-parse HEAD)
resolve_finding AUDIT-COMMON-901
commit_production AUDIT-COMMON-901
expect_pass "$base" env AUDIT_REQUIRE_COMPLETE=0

# A referenced finding must be resolved by the candidate.
new_case
append_finding AUDIT-COMMON-902 MAJOR OPEN
commit_docs "docs: record unresolved finding"
base=$(git rev-parse HEAD)
commit_production AUDIT-COMMON-902
expect_fail "$base" "candidate status is OPEN" env AUDIT_REQUIRE_COMPLETE=0

# A finding introduced together with production code did not exist in the base.
new_case
base=$(git rev-parse HEAD)
append_finding AUDIT-COMMON-903 MAJOR RESOLVED
commit_production AUDIT-COMMON-903
expect_fail "$base" "was not uniquely present in the event base" env AUDIT_REQUIRE_COMPLETE=0

# An already resolved finding cannot authorize another production change.
new_case
append_finding AUDIT-COMMON-904 MAJOR RESOLVED
commit_docs "docs: record resolved finding"
base=$(git rev-parse HEAD)
commit_production AUDIT-COMMON-904
expect_fail "$base" "non-actionable base status RESOLVED" env AUDIT_REQUIRE_COMPLETE=0

# An unrelated open finding does not block a valid independent fix.
new_case
append_finding AUDIT-COMMON-905 BLOCKER OPEN
append_finding AUDIT-COMMON-906 MAJOR OPEN
commit_docs "docs: record independent findings"
base=$(git rev-parse HEAD)
resolve_finding AUDIT-COMMON-906
commit_production AUDIT-COMMON-906
expect_pass "$base" env AUDIT_REQUIRE_COMPLETE=0

# Completion mode rejects open major findings and accepts a complete ledger.
new_case
base=$(git rev-parse HEAD)
append_finding AUDIT-COMMON-907 MAJOR OPEN
commit_docs "docs: record completion finding"
expect_fail "$base" "unresolved BLOCKER or MAJOR" env AUDIT_REQUIRE_COMPLETE=1
resolve_finding AUDIT-COMMON-907
commit_docs "docs: resolve completion finding"
expect_pass "$base" env AUDIT_REQUIRE_COMPLETE=1

# Pull requests still require trailers on every production commit.
new_case
append_finding AUDIT-COMMON-908 MAJOR OPEN
commit_docs "docs: record trailer finding"
base=$(git rev-parse HEAD)
resolve_finding AUDIT-COMMON-908
commit_production_without_trailers
expect_fail "$base" "lacks Audit and Contract trailers" \
	env POLICY_EVENT_NAME=pull_request AUDIT_REQUIRE_COMPLETE=0

# A squashed main commit is authorized by its parent-to-candidate transition.
new_case
append_finding AUDIT-COMMON-909 MAJOR OPEN
commit_docs "docs: record squash finding"
base=$(git rev-parse HEAD)
resolve_finding AUDIT-COMMON-909
commit_production_without_trailers
expect_pass "$base" env POLICY_EVENT_NAME=push AUDIT_REQUIRE_COMPLETE=0

# A main production commit without an actionable transition is rejected.
new_case
base=$(git rev-parse HEAD)
commit_production_without_trailers
expect_fail "$base" "does not resolve a finding that was OPEN or IN_PROGRESS in the event base" \
	env POLICY_EVENT_NAME=push AUDIT_REQUIRE_COMPLETE=0

# A finding created and resolved only in the squashed candidate is not prior
# authorization for production work.
new_case
base=$(git rev-parse HEAD)
append_finding AUDIT-COMMON-910 MAJOR RESOLVED
commit_production_without_trailers
expect_fail "$base" "does not resolve a finding that was OPEN or IN_PROGRESS in the event base" \
	env POLICY_EVENT_NAME=push AUDIT_REQUIRE_COMPLETE=0

echo "policy self-test: audit ledger transition cases passed"
