import { mkdir, rename } from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";
import { assert, bound, fresh, owner, sha256 } from "./desktop-repair/process.mjs";
import { verifyResolvedMedia } from "./desktop-repair/media.mjs";
import { writeUndoScript } from "./desktop-repair/delivery.mjs";
import { assertRepairHistory, assertUndoHistory, parseHistory } from "./desktop-repair/history.mjs";

// Maintainer CI entrypoint. Inputs are data, never interpolated shell commands.
const source = process.cwd();
const revision = process.env.HANDOFF_SOURCE_COMMIT;
const goldenRevision = process.env.HANDOFF_GOLDEN_COMMIT;
const output = process.env.HANDOFF_OUTPUT;
assert(process.platform === "darwin", "native macOS qualification required");
assert(process.env.HANDOFF_EXPECTED_ARCH === process.arch, "native host architecture differs from the reviewed matrix job");
assert(/^[0-9a-f]{40}$/.test(revision ?? "") && /^[0-9a-f]{40}$/.test(goldenRevision ?? ""), "explicit full source and golden revisions required");
assert(isAbsolute(output ?? "") && resolve(output).startsWith(`${source}/.tmp/`), "fresh task-owned absolute .tmp output required");
await fresh(output);
const evidence = join(output, "evidence");
const gotmp = join(output, "gotmp");
await mkdir(evidence);
await mkdir(gotmp);
const env = { ...process.env, GOMAXPROCS: "2", GOFLAGS: "-p=1 -tags=pcm_native", GOTMPDIR: gotmp, ENGINE_LIBRARY_PATHS: "" };
const processes = owner(evidence, env);
const writeJSON = (path, value) => Bun.write(path, `${JSON.stringify(value, null, 2)}\n`);
const names = ["healthy", "reconnect", "unknown-removal", "original-deletion"];
let before;
let failure;

async function snapshot(commands = processes) {
  const readGit = async (...args) => (await commands.run(["git", ...args])).trim();
  assert(await readGit("rev-parse", "HEAD") === revision, "checkout revision differs from reviewed source");
  assert(await readGit("status", "--porcelain", "--untracked-files=all") === "", "complete committed checkout must be clean");
  const entries = (await readGit("ls-files", "--stage", "-z")).split("\0").filter(Boolean);
  const result = [];
  for (const entry of entries) {
    const match = /^(100644|100755) ([0-9a-f]{40}) 0\t(.+)$/.exec(entry);
    assert(match, "unsupported source index entry");
    const [, mode, blob, path] = match;
    const actualMode = (await commands.run(["/usr/bin/stat", "-f", "%Lp", join(source, path)])).trim();
    assert((mode === "100755") === ((Number.parseInt(actualMode, 8) & 0o111) !== 0), `source executable mode differs: ${path}`);
    const bytes = await Bun.file(join(source, path)).bytes();
    result.push({ path, mode, blob, bytes: bytes.length, sha256: sha256(bytes) });
  }
  return result;
}

async function helper(binary, inputs, root, ...args) {
  return processes.run([binary, "--inputs", inputs, "--root", root, ...args]);
}

async function repair(binary, tool, inputs, root, phase) {
  await helper(tool, inputs, root, "seed");
  // This complete barrier precedes the first repair in either independent seed.
  await helper(tool, inputs, root, "verify", "--expect", "seed");
  const manifest = await Bun.file(join(root, "manifest.json")).json();
  await writeJSON(join(evidence, `${phase}-manifest.json`), manifest);
  const reviews = new Map();
  const histories = new Map();
  const cli = (name, ...args) => processes.run([binary, "--lib-dir", join(root, name), "library", ...args]);
  for (const name of names) {
    const review = JSON.parse(await cli(name, "smartlist-repair", "preview", "--json"));
    const baseline = manifest.fixtures.find((fixture) => fixture.name === name).review;
    assert(JSON.stringify(review) === JSON.stringify(baseline), `CLI baseline differs: ${name}`);
    reviews.set(name, review);
    histories.set(name, parseHistory(await cli(name, "history")));
  }
  await writeJSON(join(evidence, `${phase}-baseline-barrier.json`), [...reviews]);
  const outcomes = {};
  for (const name of names) {
    const result = JSON.parse(await cli(name, "smartlist-repair", "apply", "--plan-token", reviews.get(name).plan_token, "--json"));
    assert(result.applied === (name !== "healthy"), `wrong repair application: ${name}`);
    assert(Boolean(result.undo_id) === (name === "reconnect"), `wrong repair Undo ownership: ${name}`);
    outcomes[name] = result;
    await helper(tool, inputs, root, "verify", "--name", name, "--expect", "repaired");
    const currentHistory = parseHistory(await cli(name, "history"));
    assertRepairHistory(histories.get(name), currentHistory, manifest.fixtures.find((fixture) => fixture.name === name), result.undo_id);
    const empty = JSON.parse(await cli(name, "smartlist-repair", "preview", "--json"));
    assert(empty.rows.length === 0 && empty.reconnected === 0 && empty.removed === 0 && empty.amended_deletes === 0, `repair not idempotent: ${name}`);
    const again = JSON.parse(await cli(name, "smartlist-repair", "apply", "--plan-token", empty.plan_token, "--json"));
    assert(again.applied === false && !again.undo_id, `idempotent apply enrolled Undo: ${name}`);
    assert(JSON.stringify(parseHistory(await cli(name, "history"))) === JSON.stringify(currentHistory), `idempotent apply changed History: ${name}`);
  }
  await writeJSON(join(evidence, `${phase}-outcomes.json`), outcomes);
  return { manifest, outcomes };
}

async function relocate(tool, inputs, root, destination, binary, phase) {
  await rename(root, destination);
  // Directory absence is observed by the OS, not inferred from our requested rename.
  const absent = Bun.spawnSync(["test", "!", "-e", root]);
  assert(absent.exitCode === 0, "original root still available after relocation");
  await helper(tool, inputs, destination, "verify", "--expect", "repaired");
  const mediaEvidence = join(evidence, phase);
  await mkdir(mediaEvidence);
  await verifyResolvedMedia(processes, binary, destination, mediaEvidence);
}

try {
  before = await snapshot();
  await writeJSON(join(evidence, "source-before.json"), before);
  const platform = {};
  for (const [name, argv] of [["macos", ["/usr/bin/sw_vers"]], ["kernel", ["/usr/bin/uname", "-a"]], ["go", ["go", "version"]], ["go_environment", ["go", "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS"]]]) platform[name] = await processes.run(argv);
  const goEnvironment = JSON.parse(platform.go_environment);
  assert(goEnvironment.GOOS === "darwin" && goEnvironment.GOARCH === (process.arch === "arm64" ? "arm64" : "amd64"), "Go native build target differs from the actual host");
  await writeJSON(join(evidence, "platform.json"), platform);
  await processes.run(["git", "merge-base", "--is-ancestor", goldenRevision, revision]);
  await processes.run(["just", "build"]);
  const binary = join(source, "bin", "engine-cli");
  const tool = join(output, "desktop-repair-handoff");
  await processes.run(["go", "build", "-o", tool, "./internal/fixturecapture/handoffcmd"]);
  await writeJSON(join(evidence, "harness.json"), { helper: await bound(tool), sources: await Promise.all(["scripts/qualify-desktop-repair-handoff.mjs", "scripts/desktop-repair/process.mjs", "scripts/desktop-repair/media.mjs", "scripts/desktop-repair/delivery.mjs", "scripts/desktop-repair/history.mjs"].map((path) => bound(join(source, path)))) });
  const metadata = await processes.run(["go", "version", "-m", binary]);
  assert(metadata.includes(`vcs.revision=${revision}`) && metadata.includes("vcs.modified=false"), "binary VCS metadata is not clean reviewed source");
  const golden = join(source, "internal/fixturecapture/testdata/desktop-5.1.0/cases.json");
  const committedGolden = await processes.run(["git", "show", `${goldenRevision}:internal/fixturecapture/testdata/desktop-5.1.0/cases.json`]);
  assert(sha256(new TextEncoder().encode(committedGolden)) === (await bound(golden)).sha256, "golden differs from reviewed committed bytes");
  const buildPath = join(evidence, "build.json");
  const binaryBinding = await bound(binary);
  await writeJSON(buildPath, { source_commit: revision, binary_sha256: binaryBinding.sha256, goos: "darwin", goarch: process.arch === "arm64" ? "arm64" : "amd64", clean: true, metadata });
  const approvalPath = join(evidence, "execution.json");
  await writeJSON(approvalPath, { source_commit: revision, binary_sha256: binaryBinding.sha256, golden_sha256: (await bound(golden)).sha256, build_sha256: (await bound(buildPath)).sha256, approved: true, workflow_run: process.env.GITHUB_RUN_ID ?? null });
  const inputs = join(evidence, "inputs.json");
  await writeJSON(inputs, { source_root: source, source_commit: revision, golden_commit: goldenRevision, golden: await bound(golden), binary: binaryBinding, build_qualification: await bound(buildPath), execution_approval: await bound(approvalPath) });
  const qualification = join(output, "qualification-seed");
  const { manifest, outcomes } = await repair(binary, tool, inputs, qualification, "qualification");
  const moved = join(output, "qualification-relocated");
  await relocate(tool, inputs, qualification, moved, binary, "qualification-media");
  await mkdir(join(moved, "bin"));
  await Bun.write(join(moved, "bin", "engine-cli"), Bun.file(binary));
  await processes.run(["chmod", "755", join(moved, "bin", "engine-cli")]);
  await writeUndoScript(moved, manifest, outcomes, binaryBinding.sha256, processes);
  for (const [name, id, state] of [["reconnect", outcomes.reconnect.undo_id, "repair-undone"], ["original-deletion", manifest.fixtures.find((f) => f.name === "original-deletion").entry_id, "original-deletion-undone"]]) {
    assert(/^[0-9a-f]{32}$/.test(id ?? ""), "missing exact qualification Undo ID");
    const history = async () => parseHistory(await processes.run([binary, "--lib-dir", join(moved, name), "library", "history"]));
    const prior = await history();
    const logs = join(moved, `undo-${name}-logs`);
    try {
      await processes.run(["/bin/sh", join(moved, "undo.sh"), name]);
    } finally {
      const retained = join(evidence, `undo-${name}`);
      await mkdir(retained);
      // Only synthetic phase evidence is exported; the temporary API state DB stays local.
      for (const file of ["binary.sha256", "api.stdout", "api.stderr", "identity.json", "history-before.stdout", "history-before.stderr", "preview.stdout", "preview.stderr", "history-after-preview.stdout", "history-after-preview.stderr", "apply.stdout", "apply.stderr"]) {
        const path = join(logs, file);
        if (await Bun.file(path).exists()) await Bun.write(join(retained, file), Bun.file(path));
      }
    }
    assert(await Bun.file(join(logs, "preview.stdout")).text() === `operation_id: ${id}\ndry_run: true\n`, "Undo preview differs from the exact delivery target");
    assert(JSON.stringify(parseHistory(await Bun.file(join(logs, "history-after-preview.stdout")).text())) === JSON.stringify(prior), "Undo preview changed History");
    const undoOutput = await Bun.file(join(moved, `undo-${name}-logs`, "apply.stdout")).text();
    assertUndoHistory(prior, await history(), undoOutput, id);
    await helper(tool, inputs, moved, "verify", "--name", name, "--expect", state);
  }
  const delivery = join(output, "delivery-seed");
  const delivered = await repair(binary, tool, inputs, delivery, "delivery");
  const bundle = join(output, "delivery");
  await relocate(tool, inputs, delivery, bundle, binary, "delivery-media");
  await mkdir(join(bundle, "bin"));
  await Bun.write(join(bundle, "bin", "engine-cli"), Bun.file(binary));
  await processes.run(["chmod", "755", join(bundle, "bin", "engine-cli")]);
  await writeUndoScript(bundle, delivered.manifest, delivered.outcomes, binaryBinding.sha256, processes);
  const originalDeletion = delivered.manifest.fixtures.find((f) => f.name === "original-deletion").entry_id;
  await writeJSON(join(bundle, "delivery.json"), { source_commit: revision, golden_commit: goldenRevision, binary_sha256: binaryBinding.sha256, desktop_accepted: false, reconnect_repair_undo: delivered.outcomes.reconnect.undo_id, original_deletion_undo: originalDeletion, cases: names });
  await Bun.write(join(bundle, "README.txt"), `Synthetic Smartlist repair fixtures. Close Engine DJ before CLI writes.
Record actual Engine DJ Desktop version, macOS version and CPU architecture.
Reference capture: Engine DJ Desktop 5.1.0, macOS 26.6.2.
Open all four cases before Undo. Record visible names, rules, titles, sibling order and audio playback.
T1 House titles and T1 House tail: Deep House / House Party / Warehouse / HOUSE.
T3 healthy Techno and T3 healthy tail: Techno.
Every case retains Stable: T3 healthy Techno -> T3 healthy tail -> ordinary Anchor.
healthy: House retains T1 House titles -> T1 House tail -> ordinary Leaf.
reconnect: the same T1 chain is under Renamed, ending at Renamed/Leaf.
unknown-removal: both broken T1 rows were explicitly reviewed for removal; no repair Undo was fabricated.
original-deletion: House/Leaf and their broken T1 rows are absent; the independent Stable chain remains.
Do not Undo before recording repaired-first observations. Then close Desktop.
From this directory, exact delivery-specific Undo commands:
./undo.sh reconnect
./undo.sh original-deletion
The script checks binary and actual library identity, previews the delivery-specific Undo, retains logs and stops on refusal.
Reconnect Undo restores its exact pre-repair damaged saved paths under House while the ordinary folder remains Renamed.
Original-deletion Undo restores House/Leaf and the complete original T1 chain, rules and memberships.
Reopen those two cases and record their actual restored states; stop and retain output on refusal or discrepancy.
Automated native qualification is not Desktop acceptance.
`);
  const archive = join(output, `desktop-repair-darwin-${process.arch}.tar.gz`);
  // Native bsdtar preserves executable modes; Bun.Archive does not expose mode creation.
  const allowlist = [...names, "bin", "README.txt", "delivery.json", "undo.sh"];
  await processes.run(["/usr/bin/tar", "-czf", archive, "-C", bundle, ...allowlist]);
  const archived = join(output, "delivery-archived");
  await rename(bundle, archived);
  assert(Bun.spawnSync(["test", "!", "-e", bundle]).exitCode === 0, "archive source root remains available");
  const extracted = join(output, "extracted");
  await fresh(extracted);
  await processes.run(["/usr/bin/tar", "-xzf", archive, "-C", extracted]);
  // Keep private full-row provenance outside the published archive, then use it for verification.
  await Bun.write(join(extracted, "manifest.json"), Bun.file(join(archived, "manifest.json")));
  await helper(tool, inputs, extracted, "verify", "--expect", "repaired");
  const extractedEvidence = join(evidence, "extracted-media");
  await mkdir(extractedEvidence);
  await verifyResolvedMedia(processes, join(extracted, "bin", "engine-cli"), extracted, extractedEvidence);
  await writeJSON(join(output, "artifact.json"), { archive: { path: archive.slice(output.length + 1), sha256: (await bound(archive)).sha256, bytes: Bun.file(archive).size }, source_commit: revision, golden_commit: goldenRevision, native_platform: `${process.platform}-${process.arch}`, desktop_accepted: false, delivery: await Bun.file(join(archived, "delivery.json")).json() });
} catch (error) {
  failure = String(error.stack ?? error);
} finally {
  try { await processes.close(); }
  catch (error) { failure = [failure, String(error.stack ?? error)].filter(Boolean).join("\n"); }
  // Use a fresh audit owner because close deliberately prevents further command issuance.
  const auditEvidence = join(evidence, "final-audit");
  await mkdir(auditEvidence);
  const audit = owner(auditEvidence, env);
  try {
    const after = await snapshot(audit);
    await writeJSON(join(evidence, "source-after.json"), after);
    assert(JSON.stringify(before) === JSON.stringify(after), "source changed during qualification");
  } catch (error) { failure = [failure, String(error.stack ?? error)].filter(Boolean).join("\n"); }
  finally { await audit.close(); }
  await writeJSON(join(output, "terminal.json"), { finished: new Date().toISOString(), passed: !failure, error: failure ?? null, desktop_accepted: false });
}
if (failure) throw new Error(failure);
