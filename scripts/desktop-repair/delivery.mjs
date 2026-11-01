import { join } from "node:path";
import { assert } from "./process.mjs";

// Generated values are restricted tokens; user paths remain quoted runtime data.
export async function writeUndoScript(root, manifest, outcomes, binarySHA256, owner) {
  const reconnect = manifest.fixtures.find((fixture) => fixture.name === "reconnect");
  const deletion = manifest.fixtures.find((fixture) => fixture.name === "original-deletion");
  const reconnectID = outcomes.reconnect.undo_id;
  assert(/^[0-9a-f]{64}$/.test(binarySHA256), "invalid delivery binary binding");
  for (const id of [reconnectID, deletion.entry_id]) assert(/^[0-9a-f]{32}$/.test(id ?? ""), "invalid exact delivery Undo ID");
  for (const id of [reconnect.uuid, deletion.uuid]) assert(/^[0-9a-f-]{36}$/.test(id ?? ""), "invalid delivery library identity");
  const script = `#!/bin/sh
set -eu
cd -- "$(dirname -- "$0")"
case "\${1-}" in
  reconnect) expected_uuid='${reconnect.uuid}'; undo_id='${reconnectID}' ;;
  original-deletion) expected_uuid='${deletion.uuid}'; undo_id='${deletion.entry_id}' ;;
  *) echo 'Usage: ./undo.sh reconnect | original-deletion' >&2; exit 2 ;;
esac
case_name=$1
logs="undo-$case_name-logs"
mkdir -- "$logs"
api_pid=''
stop_api() {
  if [ -n "$api_pid" ]; then
    kill -TERM "$api_pid" 2>/dev/null || :
    wait "$api_pid"
    api_pid=''
  fi
}
trap 'stop_api' 0
trap 'echo "Stopped; retain $logs and this bundle. Do not replay a write without reviewing its receipt." >&2; exit 1' 1 2 15
/usr/bin/shasum -a 256 ./bin/engine-cli > "$logs/binary.sha256"
read -r actual_sha remainder < "$logs/binary.sha256"
[ "$actual_sha" = '${binarySHA256}' ] || { echo 'The bundled CLI binary changed.' >&2; exit 1; }
./bin/engine-cli --lib-dir "./$case_name" api serve --port 0 --library "$(pwd)/$case_name" --state-db "$(pwd)/$logs/state.db" > "$logs/api.stdout" 2> "$logs/api.stderr" &
api_pid=$!
origin=''
attempt=0
while [ "$attempt" -lt 300 ]; do
  kill -0 "$api_pid" || { echo 'Identity API stopped before readiness.' >&2; exit 1; }
  origin=$(sed -nE 's@^(http://127\\.0\\.0\\.1:[0-9]+)$@\\1@p' "$logs/api.stdout" | head -n 1)
  [ -z "$origin" ] || break
  sleep 0.1
  attempt=$((attempt + 1))
done
[ -n "$origin" ] || { echo 'Identity API readiness deadline exceeded.' >&2; exit 1; }
/usr/bin/curl --noproxy '*' --connect-timeout 5 --max-time 15 --fail --silent --show-error "$origin/v1/libraries" > "$logs/identity.json"
actual_uuid=$(/usr/bin/plutil -extract 0.engine_uuid raw -o - "$logs/identity.json")
[ "$actual_uuid" = "$expected_uuid" ] || { echo 'Library identity differs from this delivery.' >&2; exit 1; }
stop_api
./bin/engine-cli --lib-dir "./$case_name" library history > "$logs/history-before.stdout" 2> "$logs/history-before.stderr"
./bin/engine-cli --lib-dir "./$case_name" library undo "$undo_id" --dry-run > "$logs/preview.stdout" 2> "$logs/preview.stderr"
./bin/engine-cli --lib-dir "./$case_name" library history > "$logs/history-after-preview.stdout" 2> "$logs/history-after-preview.stderr"
/usr/bin/cmp "$logs/history-before.stdout" "$logs/history-after-preview.stdout" || { echo 'Undo preview changed History.' >&2; exit 1; }
./bin/engine-cli --lib-dir "./$case_name" library undo "$undo_id" > "$logs/apply.stdout" 2> "$logs/apply.stderr"
echo "Undo completed. Retain $logs, reopen this case in Desktop and record its actual restored state."
`;
  await Bun.write(join(root, "undo.sh"), script);
  await owner.run(["chmod", "755", join(root, "undo.sh")]);
}
