import { mkdir } from "node:fs/promises";
import { join } from "node:path";

export const sha256 = (data) => new Bun.CryptoHasher("sha256").update(data).digest("hex");
export const bound = async (path) => ({ path, sha256: sha256(await Bun.file(path).bytes()) });
export const assert = (condition, message) => { if (!condition) throw new Error(message); };

export function owner(root, env) {
  const children = new Map();
  const commands = [];
  let sequence = 0;
  let interrupted = false;
  const terminate = (child) => {
    if (child.exitCode !== null) return;
    try { process.kill(-child.pid, "SIGTERM"); }
    catch (error) { if (error.code !== "ESRCH") throw error; }
  };
  const interrupt = () => { interrupted = true; for (const child of children.keys()) terminate(child); };
  process.on("SIGINT", interrupt);
  process.on("SIGTERM", interrupt);

  async function start(argv, cwd = process.cwd()) {
    assert(!interrupted, "qualification interrupted; partial evidence retained");
    const prefix = join(root, String(++sequence).padStart(4, "0"));
    const child = Bun.spawn(argv, { cwd, env, detached: true, stdin: "ignore", stdout: Bun.file(`${prefix}.stdout`), stderr: Bun.file(`${prefix}.stderr`) });
    const receipt = { argv, cwd, pid: child.pid, start: new Date().toISOString(), stdout: `${prefix}.stdout`, stderr: `${prefix}.stderr` };
    let finished;
    const joinChild = async () => {
      if (!finished) finished = (async () => {
        receipt.code = await child.exited;
        receipt.signal = child.signalCode;
        receipt.finished = new Date().toISOString();
        children.delete(child);
        commands.push(receipt);
        await Bun.write(`${prefix}.json`, `${JSON.stringify(receipt, null, 2)}\n`);
        return receipt;
      })();
      return finished;
    };
    children.set(child, joinChild);
    return { child, receipt, join: joinChild, stop: async () => { terminate(child); return joinChild(); } };
  }

  async function run(argv, cwd) {
    const operation = await start(argv, cwd);
    const result = await operation.join();
    assert(!interrupted && result.code === 0, `command failed; see ${result.stderr}`);
    return Bun.file(result.stdout).text();
  }

  async function close() {
    interrupt();
    // Every caller joins its child; the final guard also joins after any thrown assertion.
    await Promise.all([...children.values()].map((joinChild) => joinChild()));
    process.off("SIGINT", interrupt);
    process.off("SIGTERM", interrupt);
    await Bun.write(join(root, "commands.json"), `${JSON.stringify(commands, null, 2)}\n`);
  }
  return { start, run, close };
}

export async function fresh(path) {
  await mkdir(path); // Exclusive ownership: existing and partial namespaces are refused.
}
