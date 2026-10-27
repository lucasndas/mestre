import { expect, test } from "vitest";
import { en } from "./locales/en";
import { ptBR } from "./locales/pt-BR";

function flatten(o: unknown, prefix = ""): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(o as Record<string, unknown>)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (typeof v === "string") out[key] = v;
    else Object.assign(out, flatten(v, key));
  }
  return out;
}

const placeholders = (s: string) => [...s.matchAll(/{{(\w+)}}/g)].map((m) => m[1]).sort();

test("both catalogs have exactly the same keys", () => {
  expect(Object.keys(flatten(en)).sort()).toEqual(Object.keys(flatten(ptBR)).sort());
});

test("no text is empty and the placeholders of a text are the same in both languages", () => {
  const pt = flatten(ptBR);
  const e = flatten(en);
  for (const [key, text] of Object.entries(pt)) {
    expect(text.trim(), key).not.toBe("");
    expect(e[key]?.trim(), key).not.toBe("");
    expect(placeholders(e[key] ?? ""), key).toEqual(placeholders(text));
  }
});

test("every code of the API has a text (internal/panel/errors.go through the generated types)", async () => {
  const gen = await import("../gen/panel");
  const codes = Object.entries(gen)
    .filter(([name, v]) => name.startsWith("Code") && typeof v === "string" && name !== "Code")
    .map(([, v]) => v as string);
  expect(codes.length).toBeGreaterThan(10);
  for (const c of codes) expect(Object.keys(ptBR.errors), c).toContain(c);
});

// CA-JORN-13
test("no text shown in Portuguese names a campaign, the highest level or the party in English", () => {
  const allowed = ["Dono da credencial", "Dono: {{name}}"];
  for (const [key, text] of Object.entries(flatten(ptBR))) {
    const left = allowed.reduce((s, a) => s.replaceAll(a, ""), text).replace(/{{\w+}}/g, "");
    expect(left, key).not.toMatch(/\b(Workspaces?|workspaces?|Party|Dono)\b/);
  }
  expect(ptBR.shell.nav.workspaces).toBe("Campanhas");
  expect(ptBR.errors.level_owner).toBe("Senhor da Guilda");
  expect(ptBR.shell.nav.ceiling).toBe("Teto da campanha");
  expect(ptBR.workspace.block.party).toBe("Companhia");
});

// CA-JORN-14
test("the English panel says Guildmaster and Campaign, and keeps Party", () => {
  for (const [key, text] of Object.entries(flatten(en))) {
    const left = text
      .replace(/{{\w+}}/g, "")
      .replaceAll("Owner of the credential", "")
      .replaceAll("Owner: ", "");
    expect(left, key).not.toMatch(/\b(Workspaces?|workspaces?|Owner)\b/);
  }
  expect(en.errors.level_owner).toBe("Guildmaster");
  expect(en.shell.nav.workspaces).toBe("Campaigns");
  expect(en.shell.nav.ceiling).toBe("Campaign ceiling");
  expect(en.workspace.block.party).toBe("Party");
});
