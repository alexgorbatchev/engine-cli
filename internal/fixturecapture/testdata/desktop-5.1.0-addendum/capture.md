---
created_on: 2026-10-31 20:30
last_modified: 2026-10-31 20:30
status: current
---

# Desktop 5.1.0 Smartlist addendum

This data is for maintainers implementing Smartlist evaluation. It fills the comment and BPM gaps left by the `desktop-5.1.0` capture and does not replace it. Agent-driven UI automation built one new library in Engine DJ Desktop 5.1.0 on macOS 26.6.2 on October 31, 2026, using the corrected pack in `../desktop-5.1.0/pack`.

## Evidence

`cases.json` uses the `desktop-5.1.0` schema. It holds the exact rule text of 9 Smartlists and the 31 tracks' filter-relevant values, read from a copy of the closed library. List names and rule values match the earlier capture. Track numbers are the pack's, and Desktop's row IDs are not retained. Memberships were read from Desktop's track list and status bar, and match SQL run separately on the same database.

## Findings

- With real COMM frames Desktop imports comments. Clearing one with the inline editor stores NULL rather than an empty string, and removes the tag from the audio file. Track 20 is therefore in T7 and not in T8.
- At 180 seconds the blanket 117 BPM reading from the 30-second pack is gone. Tracks 22–25 read 120, 121, 125 and 126, so both range edges of B4 are exercised and inclusive.
- Tracks 1, 9, 11, 19, 21 and 27 read exactly 3/4 of their click tempo. The cause is unknown, so use the captured metadata rather than the pack's tempos.
- BPM is stored unrounded, and the rule column rounds it. Track 21 is 89.25106048583984.

## Interpretation limits

- A cleared comment's NULL does not show how Desktop treats an empty string written by other software.
- This library has no playlists, moves, conversions or stems. Keys are Desktop's analyzed values.
- Track 12 has one stray star and track 23 has the three stars M2 needs. No other track is rated, and no list here depends on track 12's rating.
- Audio, the library and screenshots are excluded. The two hashes in `sources` bind this data to the retained private evidence.
