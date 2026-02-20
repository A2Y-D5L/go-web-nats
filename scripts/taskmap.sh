#!/usr/bin/env bash
set -euo pipefail

taskmap_file="${TASKMAP_FILE:-TASKMAP.yaml}"

if [[ ! -f "$taskmap_file" ]]; then
	echo "missing task map: $taskmap_file" >&2
	exit 1
fi

usage() {
	cat <<'USAGE'
Usage:
  scripts/taskmap.sh list
  scripts/taskmap.sh show <task-id>
  scripts/taskmap.sh files <task-id>
  scripts/taskmap.sh tests <task-id>
USAGE
}

list_tasks() {
	awk '/^  - id: / { print $3 }' "$taskmap_file"
}

extract_task_section() {
	local mode="$1"
	local task_id="$2"

	awk -v mode="$mode" -v task_id="$task_id" '
	BEGIN {
		in_task = 0
		section = ""
		found = 0
	}
	/^  - id: / {
		current = $3
		if (in_task == 1 && current != task_id) {
			exit
		}
		if (current == task_id) {
			in_task = 1
			found = 1
			if (mode == "show") {
				print "task: " task_id
			}
			next
		}
	}
	in_task == 1 {
		if ($0 ~ /^    files:/) {
			section = "files"
			if (mode == "show") {
				print "files:"
			}
			next
		}
		if ($0 ~ /^    tests:/) {
			section = "tests"
			if (mode == "show") {
				print "tests:"
			}
			next
		}
		if ($0 ~ /^      - /) {
			item = substr($0, 9)
			if (mode == "files" && section == "files") {
				print item
			} else if (mode == "tests" && section == "tests") {
				print item
			} else if (mode == "show") {
				print "  - " item
			}
		}
	}
	END {
		if (found == 0) {
			exit 2
		}
	}
	' "$taskmap_file"
}

command="${1:-}"
if [[ -z "$command" ]]; then
	usage
	exit 1
fi

case "$command" in
list)
	list_tasks
	;;
show | files | tests)
	task_id="${2:-}"
	if [[ -z "$task_id" ]]; then
		echo "task id is required for '$command'" >&2
		usage
		exit 1
	fi
	if ! extract_task_section "$command" "$task_id"; then
		echo "task not found: $task_id" >&2
		exit 1
	fi
	;;
*)
	echo "unknown command: $command" >&2
	usage
	exit 1
	;;
esac
