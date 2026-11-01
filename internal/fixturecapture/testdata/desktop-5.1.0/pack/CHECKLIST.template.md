---
created_on: 2026-10-31 18:05
last_modified: 2026-10-31 18:05
status: archived
---

# Smartlist fixture checklist

Ticket: EUI-5177d18d. Pack on the Linux box: `{{PACK_PATH}}`

The pack holds 31 synthetic tracks. You will build a throwaway library in Engine DJ, create 38 smartlists and hand the library back.

**Your real library:** Engine DJ always opens `~/Music/Engine Library`, and it has no recovered option for choosing another location. Parts 1 and 8 park your real library under another name and put it back. Your real library is untouched while parked, but finish Part 8 before using Engine DJ for anything else.

## Part 1: Park your real library

1. Quit Engine DJ.
2. Copy `{{PACK_PATH}}/tracks` to `~/Music/smartlist-pack` on the Mac.
3. Run this in Terminal. It stops if a parked copy already exists.

   ```sh
   cd ~/Music && [ -d "Engine Library" ] && [ ! -e "Engine Library.real" ] && mv "Engine Library" "Engine Library.real" && ls -d "Engine Library.real"
   ```

4. Check that the command printed `Engine Library.real`. If it printed nothing, stop and find out why before opening Engine DJ.

## Part 2: Build the library

1. Open Engine DJ. It creates a new empty `~/Music/Engine Library`.
2. In Preferences, under Analysis, turn **Auto Analysis** off. Leave Key Notation and BPM Range alone. Note what Auto Analysis was set to so you can restore it.
   **Never back up this library.** Desktop overwrites its existing backup, and that backup may be your real library's. If a quit dialog shows **Backup on exit**, untick it before quitting, and note that it was ticked. Do not use Library Backup in Preferences.
3. Add every file in `~/Music/smartlist-pack` to the Collection.
4. Select every track **except** `Unanalyzed One` and `Unanalyzed Two`, then analyze them. If those two get analyzed anyway, note it: G1 and G2 then need another way to get an unanalyzed track.
   **Check before going on.** `Number One` to `Number Five` must show BPM 119, 120, 121, 125 and 126, and `Field Comment` must show the comment `Wombat`. If either is wrong, stop and report it: B1 to B4, M1, M2, T7, T8 and C1 cannot meet their predictions, so building the lists would waste the run.
5. Set ratings and keys on these 5 tracks by hand:

   | # | Title | Stars | Key (Camelot) | Key (name) |
   | --- | --- | --- | --- | --- |
{{HAND_EDIT_ROWS}}

6. Edit the comment on `Comment Cleared`: delete `clear me` and save, leaving it blank. If Desktop will not let you edit comments, skip this and note it.
7. Generate stems for `Techno`. If that is not practical, skip it and note it. Create S1 and S2 anyway.
8. Create playlists at the top level, in this order: `Alpha`, `Beta`, `Gamma`. Then create `Child` inside `Beta`.

## Part 3: Create the smartlists

Use **Create Smartlist** and name each list exactly as in the first column. Desktop has no duplicate action, so each one is created fresh. Create P2 from `Child`'s menu. Drag P3 so it sits between `Alpha` and `Beta`.

- For a single-rule list, leave Match all rules as it is.
- `import day` is the date you did Part 2. `day before import` is the date before it.
- Ranges use the two boxes: `120 to 125` means 120 in the first and 125 in the second.
- Predicted tracks are numbers from the track table at the end. They come from the static trace, not from Desktop. A mismatch is not your problem to fix: check for a typo in the rule, then leave it. It may be the finding.

| List | Rules | Predicted | Note |
| --- | --- | --- | --- |
{{SMARTLIST_ROWS}}

P1, P2 and P3 share one rule: File Type is equal to `flac`. If Desktop refuses a value or a rule, note the list name and what it said, then carry on.

## Part 4: Record what each list shows

1. Make the Engine DJ window big enough to show the list tree and about 30 track rows.
2. Sort tracks by Title, and show the Comment column if you can.
3. Click each smartlist from T1 to P3 in the order above and take a full-screen screenshot (`Cmd+Shift+3`) of each. If a list is longer than the window, scroll and take a second one.

## Part 5: Copy the `before` state

1. Quit Engine DJ, with **Backup on exit** unticked if it appears, and wait for it to fully exit. Its database is only complete once it has closed.
2. Copy the folder `~/Music/Engine Library` to `{{PACK_PATH}}/returned/before`.

## Part 6: Make the changes

1. Open Engine DJ.
2. Drag `Beta`, which holds `Child` and P2, into `Gamma`.
3. Drag P3 so it sits after `Gamma`.
4. Right-click T1 and choose **Convert to Playlist**. Note whether T1 is still there and what the new playlist is called.
5. Convert T1 again and note the second playlist's name.
6. Screenshot the list tree.
7. Open Beta > Child > P2 and check it still shows the 3 FLAC tracks. Note it if it does not.

## Part 7: Copy the `after` state

1. Quit Engine DJ, with **Backup on exit** unticked if it appears, and wait for it to fully exit.
2. Copy the folder `~/Music/Engine Library` to `{{PACK_PATH}}/returned/after`.
3. Copy your screenshots to `{{PACK_PATH}}/returned/screenshots`.
4. Write anything you noted into `{{PACK_PATH}}/returned/notes.txt`.

## Part 8: Restore your real library

1. Make sure Engine DJ is closed.
2. Run this in Terminal. It stops unless the parked library is still there.

   ```sh
   cd ~/Music && [ -d "Engine Library.real" ] && rm -rf "Engine Library" && mv "Engine Library.real" "Engine Library" && ls -d "Engine Library"
   ```

3. Check that the command printed `Engine Library`.
4. Open Engine DJ, restore Auto Analysis and Backup on exit to their old settings, and check your real collection is back.
5. Delete `~/Music/smartlist-pack`.

The command deletes only the throwaway library, so run it after Part 7 has copied it.

## Optional: Engine hardware

Skip this unless you have a device and want smartlists on it. It does not block anything.

Do this before Part 8. Export B1 to a drive and load it on the device. Back on the Mac, add one more track over 120 BPM to the drive. On the device, note whether B1 now shows that track.

## Tracks

Tags apply to every track except 31: Artist `Fixture`, Album `Pack`, Genre `Test`, unless the track's Title says it is testing that field. BPM is the click tempo. Track 31 has no tags at all.

| # | File | Title | Type | Seconds | BPM | Year |
| --- | --- | --- | --- | --- | --- | --- |
{{TRACK_ROWS}}
