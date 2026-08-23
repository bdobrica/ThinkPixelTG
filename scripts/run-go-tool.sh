#!/bin/sh
set -eu

if [ "$#" -lt 1 ]; then
	echo "usage: run-go-tool.sh TOOL [ARGS...]" >&2
	exit 2
fi

tool_name=$1
shift
tool_path=$(cd "tools/$tool_name" && go tool -n "$tool_name")
exec "$tool_path" "$@"
