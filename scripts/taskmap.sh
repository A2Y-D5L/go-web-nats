#!/usr/bin/env bash
set -euo pipefail

taskmap_file="${TASKMAP_FILE:-TASKMAP.yaml}"
taskmap_parser="${TASKMAP_PARSER:-auto}"
taskmap_cache_dir="${TASKMAP_CACHE_DIR:-${TMP_DIR:-./.tmp}}"

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

Optional environment variables:
  TASKMAP_FILE   Path to task map (default: TASKMAP.yaml)
  TASKMAP_PARSER Parser mode: auto | ruby | awk (default: auto)
USAGE
}

supports_ruby_yaml() {
	command -v ruby >/dev/null 2>&1
}

run_ruby_parser() {
	local mode="$1"
	local task_id="${2:-}"
	local cache_file
	cache_file="$(ruby_cache_file)"
	if [[ ! -f "$cache_file" ]]; then
		build_ruby_cache "$cache_file"
	fi
	case "$mode" in
	list)
		awk -F'\t' '$1 == "TASK" { print $2 }' "$cache_file"
		;;
	show | files | tests)
		if [[ -z "$task_id" ]]; then
			echo "task id is required for '$mode'" >&2
			exit 1
		fi
		if ! awk -F'\t' -v task_id="$task_id" '$1 == "TASK" && $2 == task_id { found = 1 } END { exit found ? 0 : 1 }' "$cache_file"; then
			echo "task not found: $task_id" >&2
			exit 2
		fi
		case "$mode" in
		show)
			echo "task: $task_id"
			echo "files:"
			awk -F'\t' -v task_id="$task_id" '$1 == "FILE" && $2 == task_id { print "  - " $3 }' "$cache_file"
			echo "tests:"
			awk -F'\t' -v task_id="$task_id" '$1 == "TEST" && $2 == task_id { print "  - " $3 }' "$cache_file"
			;;
		files)
			awk -F'\t' -v task_id="$task_id" '$1 == "FILE" && $2 == task_id { print $3 }' "$cache_file"
			;;
		tests)
			awk -F'\t' -v task_id="$task_id" '$1 == "TEST" && $2 == task_id { print $3 }' "$cache_file"
			;;
		esac
		;;
	*)
		echo "unknown command: $mode" >&2
		exit 1
		;;
	esac
}

ruby_cache_key() {
	cksum "$taskmap_file" | awk '{ print $1 "-" $2 }'
}

ruby_cache_file() {
	printf '%s/taskmap-cache-v1-%s.tsv\n' "$taskmap_cache_dir" "$(ruby_cache_key)"
}

build_ruby_cache() {
	local cache_file="$1"
	local tmp_cache
	mkdir -p "$taskmap_cache_dir"
	tmp_cache="$(mktemp "$taskmap_cache_dir/taskmap-cache.XXXXXX")"
	trap 'rm -f "$tmp_cache"' EXIT
	ruby - "$taskmap_file" >"$tmp_cache" <<'RUBY'
require "yaml"

taskmap_file = ARGV[0]

begin
  raw = File.read(taskmap_file)
rescue Errno::ENOENT
  warn "missing task map: #{taskmap_file}"
  exit 1
end

begin
  data = YAML.safe_load(raw, permitted_classes: [], aliases: false) || {}
rescue Psych::Exception => e
  warn "invalid task map yaml: #{e.message}"
  exit 1
end

unless data.is_a?(Hash)
  warn "invalid task map: expected top-level mapping"
  exit 1
end

tasks = data["tasks"]
unless tasks.is_a?(Array)
  warn "invalid task map: expected 'tasks' to be a list"
  exit 1
end

normalize_list = lambda do |value, field_name, task_name|
  return [] if value.nil?
  unless value.is_a?(Array) && value.all? { |item| item.is_a?(String) }
    warn "invalid task entry for #{task_name}: '#{field_name}' must be a list of strings"
    exit 1
  end
  value
end

tasks.each do |task|
  unless task.is_a?(Hash)
    warn "invalid task map: each task entry must be a mapping"
    exit 1
  end

  id = task["id"]
  unless id.is_a?(String) && !id.empty?
    warn "invalid task map: each task must define a non-empty string id"
    exit 1
  end

  files = normalize_list.call(task["files"], "files", id)
  tests = normalize_list.call(task["tests"], "tests", id)

  puts ["TASK", id].join("\t")
  files.each { |item| puts ["FILE", id, item].join("\t") }
  tests.each { |item| puts ["TEST", id, item].join("\t") }
end
RUBY
	mv "$tmp_cache" "$cache_file"
	trap - EXIT
}

list_tasks_awk() {
	awk '
	function trim(s) {
		sub(/^[[:space:]]+/, "", s)
		sub(/[[:space:]]+$/, "", s)
		return s
	}
	function sanitize(s) {
		sub(/[[:space:]]*#.*/, "", s)
		s = trim(s)
		if (s ~ /^".*"$/) {
			s = substr(s, 2, length(s) - 2)
		}
		return trim(s)
	}
	BEGIN {
		in_tasks = 0
	}
	{
		if ($0 ~ /^[[:space:]]*tasks:[[:space:]]*$/) {
			in_tasks = 1
			next
		}
		if (in_tasks == 0) {
			next
		}
		if ($0 ~ /^[[:space:]]*verification:[[:space:]]*$/) {
			in_tasks = 0
			next
		}
		if ($0 ~ /^[[:space:]]*-[[:space:]]*id:[[:space:]]*/) {
			id = $0
			sub(/^[[:space:]]*-[[:space:]]*id:[[:space:]]*/, "", id)
			id = sanitize(id)
			if (id != "") {
				print id
			}
		}
	}
	' "$taskmap_file"
}

extract_task_section_awk() {
	local mode="$1"
	local task_id="$2"

	awk -v mode="$mode" -v task_id="$task_id" '
	function trim(s) {
		sub(/^[[:space:]]+/, "", s)
		sub(/[[:space:]]+$/, "", s)
		return s
	}
	function sanitize(s) {
		sub(/[[:space:]]*#.*/, "", s)
		s = trim(s)
		if (s ~ /^".*"$/) {
			s = substr(s, 2, length(s) - 2)
		}
		return trim(s)
	}
	BEGIN {
		in_tasks = 0
		in_task = 0
		section = ""
		found = 0
	}
	{
		if ($0 ~ /^[[:space:]]*tasks:[[:space:]]*$/) {
			in_tasks = 1
			next
		}
		if (in_tasks == 0) {
			next
		}
		if ($0 ~ /^[[:space:]]*verification:[[:space:]]*$/) {
			in_tasks = 0
			next
		}
	}
	$0 ~ /^[[:space:]]*-[[:space:]]*id:[[:space:]]*/ {
		current = $0
		sub(/^[[:space:]]*-[[:space:]]*id:[[:space:]]*/, "", current)
		current = sanitize(current)
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
		in_task = 0
		next
	}
	in_task == 1 {
		if ($0 ~ /^[[:space:]]*files:[[:space:]]*$/) {
			section = "files"
			if (mode == "show") {
				print "files:"
			}
			next
		}
		if ($0 ~ /^[[:space:]]*tests:[[:space:]]*$/) {
			section = "tests"
			if (mode == "show") {
				print "tests:"
			}
			next
		}
		if ($0 ~ /^[[:space:]]*-[[:space:]]*/) {
			item = $0
			sub(/^[[:space:]]*-[[:space:]]*/, "", item)
			item = sanitize(item)
			if (item == "") {
				next
			}
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

run_awk_parser() {
	local mode="$1"
	local task_id="${2:-}"
	case "$mode" in
	list)
		list_tasks_awk
		;;
	show | files | tests)
		if [[ -z "$task_id" ]]; then
			echo "task id is required for '$mode'" >&2
			exit 1
		fi
		if ! extract_task_section_awk "$mode" "$task_id"; then
			echo "task not found: $task_id" >&2
			exit 1
		fi
		;;
	*)
		echo "unknown command: $mode" >&2
		exit 1
		;;
	esac
}

run_parser() {
	local mode="$1"
	local task_id="${2:-}"
	case "$taskmap_parser" in
	ruby)
		run_ruby_parser "$mode" "$task_id"
		;;
	awk)
		run_awk_parser "$mode" "$task_id"
		;;
	auto)
		if supports_ruby_yaml; then
			if ! run_ruby_parser "$mode" "$task_id"; then
				run_awk_parser "$mode" "$task_id"
			fi
		else
			run_awk_parser "$mode" "$task_id"
		fi
		;;
	*)
		echo "invalid TASKMAP_PARSER value: $taskmap_parser (expected auto|ruby|awk)" >&2
		exit 1
		;;
	esac
}

command="${1:-}"
if [[ -z "$command" ]]; then
	usage
	exit 1
fi

case "$command" in
list)
	run_parser list
	;;
show | files | tests)
	task_id="${2:-}"
	if [[ -z "$task_id" ]]; then
		echo "task id is required for '$command'" >&2
		usage
		exit 1
	fi
	run_parser "$command" "$task_id"
	;;
*)
	echo "unknown command: $command" >&2
	usage
	exit 1
	;;
esac
