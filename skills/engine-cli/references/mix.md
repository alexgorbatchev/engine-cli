# Engine CLI mix command reference

All commands inherit the root flags documented in [SKILL.md](../SKILL.md).

## `engine-cli mix`

Extract tracklists and acquire ordered mixes

Usage: `engine-cli mix [flags]`.

- `--help, -h` (bool; default `false`): help for mix.

## `engine-cli mix candidates`

Inspect or acquire a mix using native extraction

Usage: `engine-cli mix candidates <query> [flags]`.

- `--auto-install` (bool; default `false`): Install missing upstream dependencies.
- `--extractor` (string; default ``): Tracklist extractor: auto or youtube-comments.
- `--help, -h` (bool; default `false`): help for candidates.
- `--json` (bool; default `false`): Print compact structured results.
- `--llm-model` (string; default ``): AI model used to extract tracklists.
- `--llm-provider` (string; default ``): AI provider used to extract tracklists.
- `--no-cache` (bool; default `false`): Bypass cached results.
- `--skip-metadata` (bool; default `false`): Skip online metadata lookup.
- `--skip-verify` (bool; default `false`): Skip audio quality verification.
- `--sources` (string; default ``): Comma-separated audio sources.

## `engine-cli mix fetch`

Inspect or acquire a mix using native extraction

Usage: `engine-cli mix fetch <query> [flags]`.

- `--auto-install` (bool; default `false`): Install missing upstream dependencies.
- `--candidate-url` (string; default ``): URL from the selected candidate set.
- `--extractor` (string; default ``): Tracklist extractor: auto or youtube-comments.
- `--help, -h` (bool; default `false`): help for fetch.
- `--import` (bool; default `false`): Import successful downloads into the collection.
- `--json` (bool; default `false`): Print compact structured results.
- `--llm-model` (string; default ``): AI model used to extract tracklists.
- `--llm-provider` (string; default ``): AI provider used to extract tracklists.
- `--no-cache` (bool; default `false`): Bypass cached results.
- `--selection-token` (string; default ``): Persisted candidate selection token.
- `--skip-metadata` (bool; default `false`): Skip online metadata lookup.
- `--skip-verify` (bool; default `false`): Skip audio quality verification.
- `--sources` (string; default ``): Comma-separated audio sources.
- `--to-playlist` (string; default ``): Assign imported tracks to this playlist.

## `engine-cli mix inspect`

Inspect or acquire a mix using native extraction

Usage: `engine-cli mix inspect <query> [flags]`.

- `--auto-install` (bool; default `false`): Install missing upstream dependencies.
- `--extractor` (string; default ``): Tracklist extractor: auto or youtube-comments.
- `--help, -h` (bool; default `false`): help for inspect.
- `--json` (bool; default `false`): Print compact structured results.
- `--llm-model` (string; default ``): AI model used to extract tracklists.
- `--llm-provider` (string; default ``): AI provider used to extract tracklists.
- `--no-cache` (bool; default `false`): Bypass cached results.
- `--skip-metadata` (bool; default `false`): Skip online metadata lookup.
- `--skip-verify` (bool; default `false`): Skip audio quality verification.
- `--sources` (string; default ``): Comma-separated audio sources.
