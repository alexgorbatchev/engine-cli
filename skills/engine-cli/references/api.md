# Engine CLI api command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

## `engine-cli api`

Access your library through a local HTTP API

Usage: `engine-cli api [flags]`.

- `--help, -h` (bool; default `false`): help for api.

## `engine-cli api serve`

Start the HTTP API on this machine

Usage: `engine-cli api serve [flags]`.

Expose all library, playlist, and track operations on 127.0.0.1.
Jobs report progress through JSON status and server-sent events.
Close Engine DJ before changing or restoring your library.
Job status and progress are cleared when this server stops. Library undo history is stored separately.

GET /v1/operations lists accepted parameters. POST /v1/operations/{name} starts a job; GET /v1/jobs/{id} reports its result and GET /v1/jobs/{id}/events streams resumable progress. POST /v1/jobs/{id}/cancel requests cancellation. Job status and progress last for the server process lifetime; library undo history is stored separately.

- `--allow-origin` (stringSlice; default `[]`): Exact browser origins permitted to access the API, such as http://localhost:3000.
- `--help, -h` (bool; default `false`): help for serve.
- `--library` (stringArray; default `[]`): Open an existing library; repeat for multiple paths (overrides semicolon-separated ENGINE_LIBRARY_PATHS).
- `--port` (int; default `8787`): Local HTTP port (0 selects an available port).
- `--state-db` (string; default ``): Persistent library preferences database (default: XDG state directory).
