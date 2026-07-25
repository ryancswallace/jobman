#!/bin/sh

set -eu

usage() {
	cat >&2 <<'EOF'
usage:
  devel/screencasts.sh check
  devel/screencasts.sh dependencies
  devel/screencasts.sh render VHS [TAPE...]
EOF
	exit 2
}

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
screencast_root=$repo_root/docs/screencaps
tape_root=$screencast_root/tape

normalize_tape() {
	name=${1##*/}
	name=${name%.tape}
	name=${name%.gif}
	name=${name%.webm}
	case $name in
		'' | *[!A-Za-z0-9_-]*)
			echo "invalid tape name: $1" >&2
			exit 2
			;;
	esac
	printf '%s\n' "$name"
}

check_tape() {
	name=$1
	tape=$tape_root/$name.tape

	if [ ! -f "$tape" ]; then
		echo "missing tape: $tape" >&2
		return 1
	fi
	if ! grep -Fxq "Output gif/$name.gif" "$tape"; then
		echo "$tape must declare Output gif/$name.gif" >&2
		return 1
	fi
	if ! grep -Fxq "Output webm/$name.webm" "$tape"; then
		echo "$tape must declare Output webm/$name.webm" >&2
		return 1
	fi
	if [ ! -s "$screencast_root/gif/$name.gif" ]; then
		echo "missing or empty screencast: docs/screencaps/gif/$name.gif" >&2
		return 1
	fi
	if [ ! -s "$screencast_root/webm/$name.webm" ]; then
		echo "missing or empty screencast: docs/screencaps/webm/$name.webm" >&2
		return 1
	fi
}

check_inventory() {
	for tape in "$tape_root"/*.tape; do
		if [ ! -e "$tape" ]; then
			echo "no VHS tapes found under docs/screencaps/tape" >&2
			return 1
		fi
		check_tape "$(normalize_tape "$tape")"
	done

	for media in "$screencast_root"/gif/*.gif "$screencast_root"/webm/*.webm; do
		[ -e "$media" ] || continue
		name=$(normalize_tape "$media")
		if [ ! -f "$tape_root/$name.tape" ]; then
			echo "screencast has no matching tape: ${media#"$repo_root"/}" >&2
			return 1
		fi
	done
}

check_dependencies() {
	for dependency in zsh ttyd ffmpeg; do
		if ! command -v "$dependency" >/dev/null 2>&1; then
			echo "rendering screencasts requires $dependency on PATH" >&2
			return 2
		fi
	done
}

render_tape() {
	vhs=$1
	name=$2
	tape=$tape_root/$name.tape

	if [ ! -f "$tape" ]; then
		echo "unknown tape: $name" >&2
		return 2
	fi

	echo "Rendering docs/screencaps/tape/$name.tape"
	(
		cd "$screencast_root"
		"$vhs" "tape/$name.tape"
	)
	check_tape "$name"
}

case ${1-} in
	check)
		[ "$#" -eq 1 ] || usage
		check_inventory
		;;
	dependencies)
		[ "$#" -eq 1 ] || usage
		check_dependencies
		;;
	render)
		[ "$#" -ge 2 ] || usage
		vhs=$2
		shift 2

		if [ ! -x "$vhs" ]; then
			echo "VHS executable not found: $vhs" >&2
			exit 2
		fi
		check_dependencies
		if [ ! -x "$repo_root/bin/jobman" ]; then
			echo "rendering screencasts requires bin/jobman; run make build" >&2
			exit 2
		fi

		if [ "$#" -eq 0 ]; then
			for tape in "$tape_root"/*.tape; do
				render_tape "$vhs" "$(normalize_tape "$tape")"
			done
		else
			for tape in "$@"; do
				render_tape "$vhs" "$(normalize_tape "$tape")"
			done
		fi
		check_inventory
		;;
	*)
		usage
		;;
esac
