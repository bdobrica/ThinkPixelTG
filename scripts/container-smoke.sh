#!/bin/sh
set -eu

image=${1:-thinkpixeltg:dev}
container_name="thinkpixeltg-smoke-$$"

cleanup() {
	docker rm --force "$container_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

test "$(docker image inspect --format '{{.Config.User}}' "$image")" = "65532:65532"
test "$(docker image inspect --format '{{json .Config.Entrypoint}}' "$image")" = '["/thinkpixeltg"]'

docker run --detach --name "$container_name" --read-only --cap-drop=ALL \
	--security-opt=no-new-privileges --tmpfs /tmp:rw,noexec,nosuid,size=16m \
	--publish 127.0.0.1:18080:8080 "$image" >/dev/null

attempt=1
while [ "$attempt" -le 30 ]; do
	if curl --fail --silent http://127.0.0.1:18080/livez >/dev/null; then break; fi
	attempt=$((attempt + 1)); sleep 1
done
test "$attempt" -le 30

if docker exec "$container_name" /bin/sh -c true >/dev/null 2>&1; then
	echo "runtime unexpectedly contains a shell" >&2
	exit 1
fi

docker stop --time 10 "$container_name" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$container_name")" = "0"
