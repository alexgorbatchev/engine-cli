import { join } from "node:path";
import { assert, sha256 } from "./process.mjs";

// Use the real CLI audio endpoint: inspecting stored filenames alone cannot prove relocation.
export async function verifyResolvedMedia(owner, binary, root, evidence) {
  const manifest = await Bun.file(join(root, "manifest.json")).json();
  const observations = [];
  for (const fixture of manifest.fixtures) {
    const library = join(root, fixture.name);
    const ids = Array.from({ length: 31 }, (_, index) => String(index + 1));
    const inspected = JSON.parse(await owner.run([binary, "--lib-dir", library, "track", "inspect", ...ids, "--json"]));
    assert(inspected.items?.length === 31, "relocated CLI inspection did not return every synthetic track");
    assert(inspected.items.every((item) => !item.error), "relocated CLI inspection reported a track failure");
    const operation = await owner.start([binary, "--lib-dir", library, "api", "serve", "--port", "0", "--library", library, "--state-db", join(evidence, `${fixture.name}-state.db`)]);
    try {
      let origin;
      const deadline = performance.now() + 30_000;
      while (performance.now() < deadline) {
        const output = await Bun.file(operation.receipt.stdout).text().catch(() => "");
        origin = output.trim().split("\n").find((line) => /^http:\/\/127\.0\.0\.1:\d+$/.test(line));
        if (origin) break;
        assert(operation.child.exitCode === null, "media API exited before readiness");
        await Bun.sleep(25);
      }
      assert(origin, "media API readiness deadline exceeded");
      const librariesResponse = await fetch(`${origin}/v1/libraries`);
      assert(librariesResponse.ok, "library discovery refused during media verification");
      const libraries = await librariesResponse.json();
      const selected = libraries.find((entry) => entry.engine_uuid === fixture.uuid);
      assert(selected, "actual API library identity missing");
      const audio = join(library, "Music", "fixture-01.wav");
      const withheld = `${audio}.withheld`;
      await owner.run(["mv", audio, withheld]);
      try {
        const missing = await fetch(`${origin}/v1/libraries/${selected.key}/tracks/1/audio`);
        assert(!missing.ok, "missing bundled audio was incorrectly served from another root");
        observations.push({ name: fixture.name, negative: "bundled-audio-unavailable", status: missing.status });
        await missing.body?.cancel();
      } finally {
        await owner.run(["mv", withheld, audio]);
      }
      for (let id = 1; id <= 31; id++) {
        const response = await fetch(`${origin}/v1/libraries/${selected.key}/tracks/${id}/audio`);
        assert(response.ok, `resolved audio unavailable: ${fixture.name} track ${id}, HTTP ${response.status}`);
        const actual = new Uint8Array(await response.arrayBuffer());
        const expected = await Bun.file(join(library, "Music", `fixture-${String(id).padStart(2, "0")}.wav`)).bytes();
        assert(sha256(actual) === sha256(expected), `resolved audio differs: ${fixture.name} track ${id}`);
        observations.push({ name: fixture.name, track_id: id, status: response.status, bytes: actual.length, sha256: sha256(actual) });
      }
    } finally {
      const stopped = await operation.stop();
      assert(stopped.code === 0, "media API did not stop cleanly");
    }
  }
  await Bun.write(join(evidence, "resolved-media.json"), `${JSON.stringify(observations, null, 2)}\n`);
  return observations;
}
