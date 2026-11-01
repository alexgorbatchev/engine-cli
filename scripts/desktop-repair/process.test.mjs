import { afterEach, expect, test } from "bun:test";
import { mkdir, mkdtemp, readdir, rm } from "node:fs/promises";
import { join } from "node:path";
import { fresh, owner } from "./process.mjs";

const roots = [];

async function temporaryRoot() {
  const parent = join(import.meta.dir, "../../.tmp/desktop-repair-process-tests");
  await mkdir(parent, { recursive: true });
  const root = await mkdtemp(join(parent, "run-"));
  roots.push(root);
  return root;
}

afterEach(async () => {
  for (const root of roots.splice(0)) await rm(root, { recursive: true });
});

test("fresh creates missing parents and owns a usable empty output", async () => {
  const root = await temporaryRoot();
  const output = join(root, "clean-checkout", ".tmp", "delivery");
  await fresh(output);
  expect(await readdir(output)).toEqual([]);
  const processes = owner(output, process.env);
  try {
    expect(await processes.run([process.execPath, "-e", 'console.log("owned output")'])).toBe("owned output\n");
  } finally {
    await processes.close();
  }
  const commands = await Bun.file(join(output, "commands.json")).json();
  expect(commands).toHaveLength(1);
  expect(commands[0].code).toBe(0);
  const names = await readdir(output);
  await expect(fresh(output)).rejects.toMatchObject({ code: "EEXIST" });
  expect(await readdir(output)).toEqual(names);
});

test.each(["empty partial", "populated partial"])("fresh refuses existing %s output unchanged", async (kind) => {
  const root = await temporaryRoot();
  const output = join(root, "delivery");
  await mkdir(output);
  const sentinel = join(output, "partial-evidence.json");
  if (kind === "populated partial") await Bun.write(sentinel, '{"unfinished":true}\n');
  const names = await readdir(output);
  await expect(fresh(output)).rejects.toMatchObject({ code: "EEXIST" });
  expect(await readdir(output)).toEqual(names);
  if (kind === "populated partial") expect(await Bun.file(sentinel).text()).toBe('{"unfinished":true}\n');
});

test("concurrent fresh attempts grant exactly one output owner", async () => {
  const root = await temporaryRoot();
  const output = join(root, "missing", "delivery");
  const results = await Promise.allSettled([fresh(output), fresh(output)]);
  expect(results.filter((result) => result.status === "fulfilled")).toHaveLength(1);
  const refused = results.find((result) => result.status === "rejected");
  expect(refused.reason.code).toBe("EEXIST");
  expect(await readdir(output)).toEqual([]);
});

test("fresh refuses a file target and preserves its bytes", async () => {
  const root = await temporaryRoot();
  const output = join(root, "delivery");
  await Bun.write(output, "existing evidence\n");
  await expect(fresh(output)).rejects.toMatchObject({ code: "EEXIST" });
  expect(await Bun.file(output).text()).toBe("existing evidence\n");
});

test("fresh refuses a parent file without replacing it", async () => {
  const root = await temporaryRoot();
  const parent = join(root, "parent");
  await Bun.write(parent, "parent evidence\n");
  const result = await Promise.allSettled([fresh(join(parent, "delivery"))]);
  expect(result[0].status).toBe("rejected");
  expect(["EEXIST", "ENOTDIR"]).toContain(result[0].reason.code);
  expect(await Bun.file(parent).text()).toBe("parent evidence\n");
  expect(await readdir(root)).toEqual(["parent"]);
});
