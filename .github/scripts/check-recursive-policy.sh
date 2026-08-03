#!/bin/sh

set -eu

initial_fencing_base=d323a68b31cf4a5043d76f95680777b5fa5f6696
merged_fencing_base=a9b0c5b675aa16c048f450a815fed8d51e882579
event_base=${POLICY_BASE_SHA:-}
policy_event_name=${POLICY_EVENT_NAME:-pull_request}
contract_tag=
if candidate_contract_tag=$(git rev-parse -q --verify refs/tags/recursive-watch-contract-v1^{commit}) &&
	git merge-base --is-ancestor "$candidate_contract_tag" HEAD; then
	contract_tag=$candidate_contract_tag
fi

case "$event_base" in
	""|0000000000000000000000000000000000000000)
		if [ -n "$contract_tag" ]; then
			event_base=HEAD
		else
			event_base=$merged_fencing_base
		fi
		;;
esac
if ! git cat-file -e "$event_base^{commit}" 2>/dev/null; then
	echo "policy: event base commit $event_base is unavailable" >&2
	exit 1
fi
if ! git merge-base --is-ancestor "$event_base" HEAD; then
	echo "policy: event base $event_base is not an ancestor of HEAD" >&2
	exit 1
fi

case "$policy_event_name" in
	pull_request|push|workflow_dispatch) ;;
	*)
		echo "policy: unsupported event name: $policy_event_name" >&2
		exit 1
		;;
esac

base=$event_base
if [ -n "$contract_tag" ]; then
	base=$contract_tag
else
	if ! git merge-base --is-ancestor "$initial_fencing_base" "$merged_fencing_base"; then
		echo "policy: merged fencing base does not descend from the initial fencing base" >&2
		exit 1
	fi
	if ! git merge-base --is-ancestor "$merged_fencing_base" "$base"; then
		echo "policy: pre-freeze base $base does not descend from merged fencing base $merged_fencing_base" >&2
		exit 1
	fi
	if ! git merge-base --is-ancestor "$base" HEAD; then
		echo "policy: comparison base $base is not an ancestor of HEAD" >&2
		exit 1
	fi
fi

range="$base..HEAD"
frozen_files="
AGENTS.md
RECURSIVE_WATCH_CONTRACT.md
recursive_contract_test.go
recursive_exception_policy_test.go
.github/policy/recursive-platform-exceptions.json
"
required_files="
AGENTS.md
QUALITY_AUDIT.md
RECURSIVE_WATCH_CONTRACT.md
RECURSIVE_WATCH_FENCING_PLAN.md
RECURSIVE_WATCH_QUALITY_PLAN.md
.github/policy/recursive-platform-exceptions.json
.github/scripts/check-recursive-policy.sh
.github/scripts/test-recursive-policy.sh
recursive_contract_test.go
recursive_exception_policy_test.go
"

for required in $required_files; do
	if [ ! -f "$required" ]; then
		echo "policy: required fencing file is missing: $required" >&2
		exit 1
	fi
done

if [ "${AUDIT_REQUIRE_COMPLETE:-0}" = 1 ] &&
	grep -Eq '^### AUDIT-.* \| (BLOCKER|MAJOR) \| (OPEN|IN_PROGRESS)$' QUALITY_AUDIT.md; then
	echo "policy: unresolved BLOCKER or MAJOR audit finding:" >&2
	grep -E '^### AUDIT-.* \| (BLOCKER|MAJOR) \| (OPEN|IN_PROGRESS)$' QUALITY_AUDIT.md >&2
	exit 1
fi

is_production_file() {
	case "$1" in
		*.go)
			case "$1" in
				*_test.go) return 1 ;;
				*) return 0 ;;
			esac
			;;
		go.mod|go.sum) return 0 ;;
		*) return 1 ;;
	esac
}

is_frozen_file() {
	for frozen in $frozen_files; do
		if [ "$1" = "$frozen" ]; then
			return 0
		fi
	done
	return 1
}

contains_reference() {
	document=$1
	reference=$2
	grep -Fq "$reference" "$document"
}

audit_heading() {
	document=$1
	reference=$2
	grep -E "^### $reference \\| (BLOCKER|MAJOR|MINOR) \\| (OPEN|IN_PROGRESS|RESOLVED|ACCEPTED_NATIVE_EVENT|ACCEPTED_NATIVE_CAPABILITY|ACCEPTED_TEST_ENVIRONMENT)$" "$document" || true
}

audit_status() {
	printf '%s\n' "$1" | awk -F ' \\| ' '{ print $3 }'
}

audit_contract_references() {
	document=$1
	reference=$2
	awk -v prefix="### $reference | " '
		/^### / {
			if (active) {
				exit
			}
			if (index($0, prefix) == 1) {
				active = 1
			}
		}
		active {
			line = $0
			while (match(line, /RC-[0-9][0-9][0-9]/)) {
				print substr(line, RSTART, RLENGTH)
				line = substr(line, RSTART + RLENGTH)
			}
		}
	' "$document" | sort -u
}

validate_push_production_candidate() {
	transitioned=
	for ref in $(
		grep -E '^### AUDIT-.* \| (BLOCKER|MAJOR|MINOR) \| (OPEN|IN_PROGRESS)$' "$base_audit" |
			awk '{ print $2 }'
	); do
		base_heading=$(audit_heading "$base_audit" "$ref")
		base_count=$(printf '%s\n' "$base_heading" | sed '/^$/d' | wc -l)
		if [ "$base_count" -ne 1 ]; then
			echo "policy: push production candidate has a non-unique event-base finding $ref" >&2
			exit 1
		fi

		candidate_heading=$(audit_heading QUALITY_AUDIT.md "$ref")
		candidate_count=$(printf '%s\n' "$candidate_heading" | sed '/^$/d' | wc -l)
		if [ "$candidate_count" -eq 1 ] &&
			[ "$(audit_status "$candidate_heading")" = RESOLVED ]; then
			transitioned="$transitioned $ref"
		fi
	done

	if [ -z "$transitioned" ]; then
		echo "policy: push production candidate does not resolve a finding that was OPEN or IN_PROGRESS in the event base" >&2
		exit 1
	fi

	for ref in $transitioned; do
		contract_refs=$(audit_contract_references QUALITY_AUDIT.md "$ref")
		if [ -z "$contract_refs" ]; then
			echo "policy: resolved push finding $ref has no Contract reference" >&2
			exit 1
		fi
		for contract_ref in $contract_refs; do
			if ! contains_reference RECURSIVE_WATCH_CONTRACT.md "### $contract_ref:"; then
				echo "policy: resolved push finding $ref references unknown contract rule $contract_ref" >&2
				exit 1
			fi
		done
	done
}

changed=$(git diff --name-only "$base" HEAD)
has_production=false
has_frozen=false
for path in $changed; do
	if is_production_file "$path"; then
		has_production=true
	fi
	if is_frozen_file "$path"; then
		has_frozen=true
	fi
done

event_has_production=false
for path in $(git diff --name-only "$event_base" HEAD); do
	if is_production_file "$path"; then
		event_has_production=true
	fi
done

policy_tmpdir=$(mktemp -d "${RUNNER_TEMP:-/tmp}/fsnotify-policy.XXXXXX")
trap 'rm -rf "$policy_tmpdir"' EXIT HUP INT TERM
base_audit=$policy_tmpdir/base-audit.md
git show "$event_base:QUALITY_AUDIT.md" >"$base_audit"

event_range="$event_base..HEAD"
referenced_audits=
if [ "$policy_event_name" = pull_request ]; then
	for commit in $(git rev-list --reverse "$event_range"); do
		commit_has_production=false
		for path in $(git diff-tree --no-commit-id --name-only -r "$commit"); do
			if is_production_file "$path"; then
				commit_has_production=true
			fi
		done
		if [ "$commit_has_production" = true ]; then
			message=$(git show -s --format=%B "$commit")
			commit_audits=$(printf '%s\n' "$message" |
				sed -n 's/^Audit:[[:space:]]*\(AUDIT-[A-Z0-9-][A-Z0-9-]*\).*$/\1/p')
			referenced_audits="$referenced_audits $commit_audits"
		fi
	done
fi

for ref in $(printf '%s\n' $referenced_audits | sort -u); do
	base_heading=$(audit_heading "$base_audit" "$ref")
	base_count=$(printf '%s\n' "$base_heading" | sed '/^$/d' | wc -l)
	if [ "$base_count" -ne 1 ]; then
		echo "policy: production change references audit finding $ref that was not uniquely present in the event base" >&2
		exit 1
	fi
	base_status=$(audit_status "$base_heading")
	case "$base_status" in
		OPEN|IN_PROGRESS) ;;
		*)
			echo "policy: production change references audit finding $ref with non-actionable base status $base_status" >&2
			exit 1
			;;
	esac

	head_heading=$(audit_heading QUALITY_AUDIT.md "$ref")
	head_count=$(printf '%s\n' "$head_heading" | sed '/^$/d' | wc -l)
	if [ "$head_count" -ne 1 ]; then
		echo "policy: production change references audit finding $ref that is not unique in the candidate ledger" >&2
		exit 1
	fi
	head_status=$(audit_status "$head_heading")
	if [ "$head_status" != RESOLVED ]; then
		echo "policy: production change must resolve audit finding $ref; candidate status is $head_status" >&2
		exit 1
	fi
done

if [ "$policy_event_name" != pull_request ] &&
	[ "$event_has_production" = true ]; then
	validate_push_production_candidate
fi

if [ "$has_production" = true ] && [ "$has_frozen" = true ]; then
	echo "policy: production code and frozen contract files changed in the same candidate" >&2
	exit 1
fi

for commit in $(git rev-list --reverse "$event_range"); do
	commit_files=$(git diff-tree --no-commit-id --name-only -r "$commit")
	commit_has_production=false
	commit_has_frozen=false
	for path in $commit_files; do
		if is_production_file "$path"; then
			commit_has_production=true
		fi
		if is_frozen_file "$path"; then
			commit_has_frozen=true
		fi
	done

	message=$(git show -s --format=%B "$commit")
	if [ "$commit_has_production" = true ]; then
		if [ "$policy_event_name" = pull_request ]; then
			audit_refs=$(printf '%s\n' "$message" |
				sed -n 's/^Audit:[[:space:]]*\(AUDIT-[A-Z0-9-][A-Z0-9-]*\).*$/\1/p')
			contract_refs=$(printf '%s\n' "$message" |
				sed -n 's/^Contract:[[:space:]]*\(RC-[0-9][0-9][0-9]\).*$/\1/p')

			if [ -z "$audit_refs" ] || [ -z "$contract_refs" ]; then
				echo "policy: production commit $commit lacks Audit and Contract trailers" >&2
				exit 1
			fi
			for ref in $audit_refs; do
				if ! contains_reference QUALITY_AUDIT.md "$ref"; then
					echo "policy: production commit $commit references unknown audit finding $ref" >&2
					exit 1
				fi
			done
			for ref in $contract_refs; do
				if ! contains_reference RECURSIVE_WATCH_CONTRACT.md "### $ref:"; then
					echo "policy: production commit $commit references unknown contract rule $ref" >&2
					exit 1
				fi
			done
		fi
	fi

	if [ "$commit_has_production" = true ] && [ "$commit_has_frozen" = true ]; then
		echo "policy: commit $commit mixes production code with frozen contract files" >&2
		exit 1
	fi
done

if git rev-parse -q --verify refs/tags/recursive-watch-contract-v1 >/dev/null; then
	contract_tag=$(git rev-list -n 1 recursive-watch-contract-v1)
	for commit in $(git rev-list --reverse "$contract_tag..HEAD"); do
		commit_has_frozen=false
		for path in $(git diff-tree --no-commit-id --name-only -r "$commit"); do
			if is_frozen_file "$path"; then
				commit_has_frozen=true
			fi
		done
		if [ "$commit_has_frozen" = true ] &&
			! git show -s --format=%B "$commit" | grep -q '^Contract-Change:[[:space:]]*approved$'; then
			echo "policy: frozen contract change $commit lacks explicit Contract-Change: approved trailer" >&2
			exit 1
		fi
	done
fi

added_sleeps=$(git diff --unified=0 "$base" HEAD -- '*_test.go' 'testdata/*' |
	sed -n '/^+++ /d; /^+.*time\.Sleep(/p; /^+[[:space:]]*sleep[[:space:]]/p')
if [ -n "$added_sleeps" ]; then
	echo "policy: new unconditional test sleeps are not permitted:" >&2
	printf '%s\n' "$added_sleeps" >&2
	exit 1
fi

extract_modules() {
	awk '
		/^require[[:space:]]+\(/ { block = 1; next }
		block && /^\)/ { block = 0; next }
		block && $1 !~ /^\/\// { print $1 }
		/^require[[:space:]]+[^(]/ { print $2 }
	' "$1" | sort -u
}

base_mod="$policy_tmpdir/base.mod"
old_modules="$policy_tmpdir/old-modules"
new_modules="$policy_tmpdir/new-modules"
git show "$base:go.mod" >"$base_mod"
extract_modules "$base_mod" >"$old_modules"
extract_modules go.mod >"$new_modules"
new_runtime_modules=$(comm -13 "$old_modules" "$new_modules")
if [ -n "$new_runtime_modules" ]; then
	echo "policy: new runtime module dependencies are not permitted:" >&2
	printf '%s\n' "$new_runtime_modules" >&2
	exit 1
fi

unformatted=$(gofmt -l -- $(git ls-files '*.go'))
if [ -n "$unformatted" ]; then
	echo "policy: gofmt is required:" >&2
	printf '%s\n' "$unformatted" >&2
	exit 1
fi

echo "policy: recursive-watch governance checks passed"
