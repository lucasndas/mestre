import { mkdir, mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, test } from "vitest";
import { emptyExceptMarker, KEPT, MARKER } from "./keep-gitkeep";

const dirs: string[] = [];
afterEach(async () => {
  await Promise.all(dirs.splice(0).map((d) => rm(d, { recursive: true, force: true })));
});

async function tmp(): Promise<string> {
  const d = await mkdtemp(join(tmpdir(), "webdist-"));
  dirs.push(d);
  return d;
}

test("emptying the output folder keeps the embed marker", async () => {
  const d = await tmp();
  await writeFile(join(d, MARKER), "");
  await writeFile(join(d, ".gitignore"), "*");
  await writeFile(join(d, "index.html"), "x");
  await mkdir(join(d, "assets"));
  await writeFile(join(d, "assets", "a.js"), "x");
  await emptyExceptMarker(d);
  expect((await readdir(d)).sort()).toEqual([...KEPT].sort());
});

test("a folder that does not exist yet is not an error", async () => {
  const d = await tmp();
  await expect(emptyExceptMarker(join(d, "missing"))).resolves.toBeUndefined();
});
