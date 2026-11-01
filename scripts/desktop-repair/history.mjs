import { assert } from "./process.mjs";

const equal = (a, b, message) => assert(JSON.stringify(a) === JSON.stringify(b), message);

export function parseHistory(text) {
  const entries = [];
  for (const line of text.split("\n")) {
    if (line === "") continue;
    if (line.startsWith("operation: ")) {
      const match = /^operation: (\S+) command: (\S+) state: (\S+) undo_of: (\S*)$/.exec(line);
      assert(match && !entries.some((entry) => entry.id === match[1]), "malformed or duplicate History header");
      entries.push({ id: match[1], command: match[2], state: match[3], undoOf: match[4], lines: [line] });
    } else {
      assert(entries.length && /^  entity: \S+ table: \S+ key: .+$/.test(line), "malformed History entity");
      entries.at(-1).lines.push(line);
    }
  }
  return entries;
}

export function assertRepairHistory(prior, current, fixture, undoID) {
  const retained = current.filter((entry) => prior.some((old) => old.id === entry.id));
  equal(retained.map((entry) => entry.id).sort(), prior.map((entry) => entry.id).sort(), "repair lost original History identities");
  const added = current.filter((entry) => !prior.some((old) => old.id === entry.id));
  if (fixture.name === "reconnect") {
    assert(added.length === 1 && added[0].id === undoID && added[0].command === "library.smartlist-repair.apply" && added[0].state === "complete" && added[0].undoOf === "", "wrong repair History ownership");
  } else equal(added, [], "repair fabricated another History entry");
  if (fixture.name !== "original-deletion") {
    equal(retained, prior, "repair changed unrelated History");
    return;
  }
  equal(current.filter((entry) => entry.id !== fixture.entry_id), prior.filter((entry) => entry.id !== fixture.entry_id), "deletion amendment changed unrelated History");
  const old = prior.find((entry) => entry.id === fixture.entry_id);
  const amended = current.find((entry) => entry.id === fixture.entry_id);
  assert(old && amended && old.lines[0] === amended.lines[0], "deletion amendment changed original header");
  assert(old.lines.slice(1).every((line) => amended.lines.includes(line)), "deletion amendment lost original entities");
  const entities = amended.lines.slice(1).filter((line) => !old.lines.includes(line)).map((line) => {
    const match = /^  entity: (\S+) table: (\S+) key: (.+)$/.exec(line);
    assert(match, "malformed deletion amendment entity");
    return { id: match[1], table: match[2], key: match[3] };
  });
  assert(entities.length === 2 && new Set(entities.map((entry) => entry.id)).size === 2 && amended.lines.length === old.lines.length + 2, "unexpected deletion amendment count");
  const expected = fixture.review.rows.map((row) => ({ table: "Smartlist", key: JSON.stringify([`SQLITE_TEXT:${Array.from(new TextEncoder().encode(row.uuid), (byte) => byte.toString(16).padStart(2, "0")).join("")}`]) }));
  equal(entities.map(({ table, key }) => ({ table, key })).sort((a, b) => a.key.localeCompare(b.key)), expected.sort((a, b) => a.key.localeCompare(b.key)), "deletion amendment differs from consented saved identities");
}

export function assertUndoHistory(prior, current, output, targetID) {
  equal(current.filter((entry) => prior.some((old) => old.id === entry.id)), prior, "Undo changed pre-existing History");
  const match = /^operation_id: ([0-9a-f]{32})\ndry_run: false\n$/.exec(output);
  const added = current.filter((entry) => !prior.some((old) => old.id === entry.id));
  assert(match && added.length === 1 && added[0].id === match[1] && added[0].undoOf === targetID && added[0].state === "complete" && added[0].command === "library.undo", "exact-ID Undo result and History ownership differ");
}
