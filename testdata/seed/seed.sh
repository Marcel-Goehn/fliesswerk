#!/usr/bin/env bash
# seed.sh resets a fixture repo for Fliesswerk's acceptance pass and creates
# the named scenarios in it. It needs only bash (3.2 or newer), git and gh, so
# it runs on Linux, macOS and Windows (Git Bash). See README.md next to it.

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: seed.sh <owner/name> [--without-setup] [<scenario>...]

Resets the fixture repo <owner/name>, pushes the fixture project to its
default branch, creates the Tracker's labels and then each named scenario.

  --without-setup  also remove .fliesswerk/ and the Factory's labels, so the
                   repo looks like it was never set up (no scenarios then)

Scenarios:
EOF
	list_scenarios | sed 's/^/  /'
}

here=$(cd "$(dirname "$0")" && pwd)
project_dir="$here/project"
scenarios_dir="$here/scenarios"

# The Factory's defaults: its branch prefix and its labels, with the
# descriptions `fliesswerk init` gives them.
branch_prefix="fliesswerk/"
factory_labels="ready-for-agent in-progress needs-investigation"
fixture_label="fixture"

label_description() {
	case $1 in
	ready-for-agent) echo "Fliesswerk may work on it" ;;
	in-progress) echo "Claimed by a Fliesswerk Run" ;;
	needs-investigation) echo "Fliesswerk handed it back to a person" ;;
	"$fixture_label") echo "Created by Fliesswerk's seed.sh; closed on the next reset" ;;
	*) echo "Used by a seed.sh scenario" ;;
	esac
}

label_color() {
	case $1 in
	ready-for-agent) echo "0E8A16" ;;
	in-progress) echo "FBCA04" ;;
	needs-investigation) echo "D93F0B" ;;
	*) echo "C5DEF5" ;;
	esac
}

die() {
	echo "seed.sh: $*" >&2
	exit 1
}

say() {
	echo "$*"
}

list_scenarios() {
	local d
	for d in "$scenarios_dir"/*/; do
		[ -f "$d/manifest" ] && basename "$d"
	done
	return 0
}

# Every command that reaches GitHub through git signs in with gh.
remote_git() {
	git -c credential.helper= -c 'credential.helper=!gh auth git-credential' "$@"
}

# strip_cr drops the carriage returns a Windows checkout or gh may add.
strip_cr() {
	tr -d '\r'
}

# ---------------------------------------------------------------------------
# Arguments

repo=""
without_setup=false
scenarios=()
for arg in "$@"; do
	case $arg in
	-h | --help)
		usage
		exit 0
		;;
	--without-setup) without_setup=true ;;
	-*) die "unknown option $arg; see --help" ;;
	*)
		if [ -z "$repo" ]; then
			repo=$arg
		else
			scenarios+=("$arg")
		fi
		;;
	esac
done

[ -n "$repo" ] || {
	usage >&2
	exit 1
}
case $repo in
*/*/* | /* | */ | *" "*) repo_ok=false ;;
*/*) repo_ok=true ;;
*) repo_ok=false ;;
esac
$repo_ok || die "name the repo as owner/name, not $repo"
if $without_setup && [ ${#scenarios[@]} -gt 0 ]; then
	die "--without-setup removes the Factory's labels, so it creates no scenarios; run \`fliesswerk init\`, then seed again without it"
fi
command -v git >/dev/null || die "needs git on the PATH"
command -v gh >/dev/null || die "needs gh on the PATH"

# ---------------------------------------------------------------------------
# Manifests
#
# A manifest has `issue` lines, an optional `exit` line and, after a line
# `dry-run:`, the output `fliesswerk run --dry-run` must show. See README.md.

# The issues of the scenario being read or created, in manifest order.
keys=()
numbers=()
ids=()

# index_of prints where key is in keys, failing if it isn't there yet.
index_of() {
	local i
	for i in ${keys[@]+"${!keys[@]}"}; do
		if [ "${keys[$i]}" = "$1" ]; then
			echo "$i"
			return 0
		fi
	done
	return 1
}

number_of() {
	local i
	i=$(index_of "$1") && echo "${numbers[$i]}"
}

id_of() {
	local i
	i=$(index_of "$1") && echo "${ids[$i]}"
}

# render replaces {{key}} with #N and {{key.number}} with N for every issue
# created so far, and {{repo}} with the repo's owner/name.
render() {
	local text=$1 i pat rep
	pat="{{repo}}"
	text=${text//"$pat"/$repo}
	for i in ${keys[@]+"${!keys[@]}"}; do
		pat="{{${keys[$i]}.number}}"
		rep="${numbers[$i]}"
		text=${text//"$pat"/$rep}
		pat="{{${keys[$i]}}}"
		rep="#${numbers[$i]}"
		text=${text//"$pat"/$rep}
	done
	printf '%s' "$text"
}

# split_list prints the comma-separated items of $1, one per line; `-` is
# none.
split_list() {
	[ "$1" = "-" ] && return 0
	printf '%s\n' "$1" | tr ',' '\n' | sed '/^$/d'
}

# check_scenario proves a scenario's manifest and bodies are complete
# before anything in the repo changes.
check_scenario() {
	local name=$1 dir="$scenarios_dir/$1" all line kind key parent blockers labels title b k seen="" n=0
	[ -f "$dir/manifest" ] || die "there is no scenario $name; there are: $(list_scenarios | tr '\n' ' ')"
	all=$(manifest_keys "$dir")
	while IFS= read -r line; do
		read -r kind key parent blockers labels title <<<"$line"
		case $kind in
		issue) ;;
		exit)
			case $key in 0 | 1) continue ;; *) die "$name/manifest: exit is 0 or 1, not $key" ;; esac
			;;
		*) die "$name/manifest: unknown line: $line" ;;
		esac
		[ -n "$title" ] || die "$name/manifest: an issue line needs a key, a parent, Blockers, labels and a title: $line"
		case " $seen " in *" $key "*) die "$name/manifest: the key $key is used twice" ;; esac
		for k in $(split_list "$parent") $(split_list "$blockers"); do
			case " $seen " in *" $k "*) ;; *) die "$name/manifest: $key names $k, which isn't an issue above it" ;; esac
		done
		[ -f "$dir/$key.md" ] || die "$name: $key has no body file $key.md"
		# shellcheck disable=SC2013 # keys hold no spaces
		for b in $(grep -o '{{[A-Za-z0-9_-]*\(\.number\)\{0,1\}}}' "$dir/$key.md" | sed 's/^{{//; s/}}$//; s/\.number$//'); do
			case " $all " in
			*" $b "*)
				case " $seen " in *" $b "*) ;; *) die "$name/$key.md refers to {{$b}}, which isn't an issue above it" ;; esac
				;;
			esac
		done
		seen="$seen $key"
		n=$((n + 1))
	done < <(manifest_lines "$dir")
	[ "$n" -gt 0 ] || die "$name/manifest has no issue lines"
	grep -q '^dry-run:' "$dir/manifest" || die "$name/manifest has no dry-run: line with the expected output"
}

# manifest_lines prints a manifest's `issue` and `exit` lines, without
# comments, blank lines or the expected output.
manifest_lines() {
	local line
	while IFS= read -r line || [ -n "$line" ]; do
		line=${line%$'\r'}
		[ "$line" = "dry-run:" ] && break
		case $line in "" | "#"*) continue ;; esac
		printf '%s\n' "$line"
	done <"$1/manifest"
}

# manifest_keys prints the keys of a manifest's issues.
manifest_keys() {
	local kind key rest
	manifest_lines "$1" | while read -r kind key rest; do
		[ "$kind" = issue ] && printf '%s ' "$key"
	done
	return 0
}

# manifest_labels prints the labels a manifest's issues carry, one per line.
manifest_labels() {
	local kind key parent blockers labels rest
	manifest_lines "$1" | while read -r kind key parent blockers labels rest; do
		[ "$kind" = issue ] && split_list "$labels"
	done
	return 0
}

# ---------------------------------------------------------------------------
# The repo

for s in ${scenarios[@]+"${scenarios[@]}"}; do
	check_scenario "$s"
done

gh auth status >/dev/null 2>&1 || die "gh isn't signed in; run \`gh auth login\` first"
gh repo view "$repo" --json name >/dev/null || die "can't read $repo with gh"
base=$(gh repo view "$repo" --json defaultBranchRef --jq '.defaultBranchRef.name // ""' | strip_cr)
[ -n "$base" ] || base=main

tmp=$(mktemp -d "${TMPDIR:-/tmp}/fwk-seed.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

say "Resetting $repo (base branch $base)"

# 1a. Close the fixture issues still open, without their Claims.
# shellcheck disable=SC2086 # one argument per label
strip_labels_jq=$(printf '"%s",' $factory_labels)
strip_labels_jq=${strip_labels_jq%,}
gh issue list --repo "$repo" --label "$fixture_label" --state open --limit 1000 \
	--json number,labels,assignees \
	--jq ".[] | [.number, ([.labels[].name | select(IN($strip_labels_jq))] | join(\",\")), ([.assignees[].login] | join(\",\"))] | join(\"\\u001f\")" |
	strip_cr >"$tmp/open.tsv"
# Fields are joined by the unit separator: with tabs, read would merge an
# empty labels field into the next.
while IFS=$'\037' read -r number labels assignees; do
	[ -n "$number" ] || continue
	edit=()
	[ -n "$labels" ] && edit+=(--remove-label "$labels")
	[ -n "$assignees" ] && edit+=(--remove-assignee "$assignees")
	if [ ${#edit[@]} -gt 0 ]; then
		gh issue edit "$number" --repo "$repo" "${edit[@]}" >/dev/null
	fi
	gh issue close "$number" --repo "$repo" --reason "not planned" >/dev/null
	say "  closed #$number"
done <"$tmp/open.tsv"

# 1b. Delete the Factory's remote branches.
work="$tmp/repo"
remote_git clone --quiet --depth 1 "https://github.com/$repo.git" "$work"
branches=()
while IFS=$'\t' read -r _ ref; do
	case $ref in "refs/heads/$branch_prefix"*) branches+=("${ref#refs/heads/}") ;; esac
done < <(remote_git -C "$work" ls-remote --heads origin | strip_cr)
if [ ${#branches[@]} -gt 0 ]; then
	remote_git -C "$work" push --quiet origin --delete "${branches[@]}"
	for b in "${branches[@]}"; do say "  deleted branch $b"; done
fi

# 1c and 2. Bring the base branch back to the fixture project, keeping a
# committed .fliesswerk/ unless --without-setup, with a new commit.
if git -C "$work" rev-parse -q --verify HEAD >/dev/null; then
	if $without_setup; then
		git -C "$work" rm -r -q --ignore-unmatch -- .
	else
		git -C "$work" rm -r -q --ignore-unmatch -- . ':(exclude).fliesswerk'
	fi
else
	git -C "$work" symbolic-ref HEAD "refs/heads/$base"
fi
cp -R "$project_dir/." "$work/"
git -C "$work" add -A
if git -C "$work" diff --cached --quiet; then
	say "  $base already holds the fixture project"
else
	identity=()
	[ -n "$(git -C "$work" config user.email || true)" ] && [ -n "$(git -C "$work" config user.name || true)" ] ||
		identity=(-c user.name="fliesswerk seed" -c user.email="seed@fliesswerk.invalid")
	git -C "$work" ${identity[@]+"${identity[@]}"} commit --quiet -m "Reset to the fixture project"
	remote_git -C "$work" push --quiet origin "HEAD:refs/heads/$base"
	say "  pushed the fixture project to $base"
fi

# 3. The labels.
gh label list --repo "$repo" --limit 1000 --json name --jq '.[].name' | strip_cr >"$tmp/labels"
has_label() {
	grep -Fxiq -- "$1" "$tmp/labels"
}
if $without_setup; then
	for l in $factory_labels; do
		if has_label "$l"; then
			gh label delete "$l" --repo "$repo" --yes >/dev/null
			say "  deleted label $l"
		fi
	done
	labels_to_create="$fixture_label"
else
	labels_to_create="$factory_labels $fixture_label"
	for s in ${scenarios[@]+"${scenarios[@]}"}; do
		labels_to_create="$labels_to_create $(manifest_labels "$scenarios_dir/$s" | tr '\n' ' ')"
	done
fi
for l in $labels_to_create; do
	if ! has_label "$l"; then
		gh label create "$l" --repo "$repo" --color "$(label_color "$l")" --description "$(label_description "$l")" >/dev/null
		echo "$l" >>"$tmp/labels"
		say "  + label $l"
	fi
done

# 4. The scenarios.

# create_issue creates one issue of a scenario and links it to its parent
# and Blockers.
create_issue() {
	local dir=$1 key=$2 parent=$3 blockers=$4 labels=$5 title=$6 args l out number id b
	render "$(strip_cr <"$dir/$key.md")" >"$tmp/body.md"
	args=(--method POST "repos/$repo/issues" -f "title=$title" -F "body=@body.md" -f "labels[]=$fixture_label")
	for l in $(split_list "$labels"); do
		args+=(-f "labels[]=$l")
	done
	# A relative path: Git Bash would rewrite an absolute one for gh.
	out=$(cd "$tmp" && gh api "${args[@]}" --jq '[.number, .id] | @tsv' | strip_cr)
	number=${out%%$'\t'*}
	id=${out#*$'\t'}
	keys+=("$key")
	numbers+=("$number")
	ids+=("$id")
	if [ "$parent" != "-" ]; then
		gh api --method POST "repos/$repo/issues/$(number_of "$parent")/sub_issues" -F "sub_issue_id=$id" --silent
	fi
	for b in $(split_list "$blockers"); do
		gh api --method POST "repos/$repo/issues/$number/dependencies/blocked_by" -F "issue_id=$(id_of "$b")" --silent
	done
	say "  #$number  $title"
}

expected="$tmp/expected"
: >"$expected"
for s in ${scenarios[@]+"${scenarios[@]}"}; do
	dir="$scenarios_dir/$s"
	keys=()
	numbers=()
	ids=()
	code=0
	say ""
	say "Scenario $s"
	while IFS= read -r line; do
		read -r kind key parent blockers labels title <<<"$line"
		case $kind in
		exit) code=$key ;;
		issue) create_issue "$dir" "$key" "$parent" "$blockers" "$labels" "$title" ;;
		esac
	done < <(manifest_lines "$dir")
	{
		echo ""
		echo "== $s: seeded alone, \`fliesswerk run --dry-run\` shows this and exits $code"
		echo ""
		render "$(sed '1,/^dry-run:/d' "$dir/manifest" | strip_cr)"
		echo ""
	} >>"$expected"
done

if [ ${#scenarios[@]} -gt 0 ]; then
	say ""
	say "Done. Pull $base in your set-up clone; with the defaults (max_parallel = 1):"
	cat "$expected"
else
	say ""
	say "Done. No scenario named; pull $base in your set-up clone."
fi
