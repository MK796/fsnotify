#!/bin/sh

set -eu

initial_fencing_base=d323a68b31cf4a5043d76f95680777b5fa5f6696
merged_fencing_base=a9b0c5b675aa16c048f450a815fed8d51e882579
base=${POLICY_BASE_SHA:-}
case "$base" in
	""|0000000000000000000000000000000000000000)
		base=$merged_fencing_base
		;;
esac
if ! git cat-file -e "$base^{commit}" 2>/dev/null; then
	echo "policy: base commit $base is unavailable" >&2
	exit 1
fi

if contract_tag=$(git rev-parse -q --verify refs/tags/recursive-watch-contract-v1^{commit}) &&
	git merge-base --is-ancestor "$contract_tag" HEAD; then
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
recursive_contract_test.go
recursive_exception_policy_test.go
"

for required in $required_files; do
	if [ ! -f "$required" ]; then
		echo "policy: required fencing file is missing: $required" >&2
		exit 1
	fi
done

if grep -Eq '^### AUDIT-.* \| (BLOCKER|MAJOR) \| (OPEN|IN_PROGRESS)$' QUALITY_AUDIT.md; then
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

if [ "$has_production" = true ] && [ "$has_frozen" = true ]; then
	echo "policy: production code and frozen contract files changed in the same candidate" >&2
	exit 1
fi

for commit in $(git rev-list --reverse "$range"); do
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

tmpdir=${RUNNER_TEMP:-/tmp}
base_mod="$tmpdir/fsnotify-policy-base.mod"
old_modules="$tmpdir/fsnotify-policy-old-modules"
new_modules="$tmpdir/fsnotify-policy-new-modules"
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
