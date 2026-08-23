#!/bin/sh
set -eu

if [ "$#" -lt 1 ]; then
	echo "usage: run-go-tool.sh TOOL [ARGS...]" >&2
	exit 2
fi

tool_name=$1
shift
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(dirname "$script_dir")
tool_path=$(cd "$repository_root/tools/$tool_name" && go tool -n "$tool_name")
exec "$tool_path" "$@"
