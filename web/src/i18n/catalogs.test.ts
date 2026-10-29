import { expect, test } from "vitest";
import { messages } from "../gen/messages";
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
  expect(messages["pt-BR"]["level.owner"]).toBe("Senhor da Guilda");
  expect(ptBR.shell.settings.entries.ceiling.name).toBe("Teto da campanha");
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
  expect(messages.en["level.owner"]).toBe("Guildmaster");
  expect(en.shell.nav.workspaces).toBe("Campaigns");
  expect(en.shell.settings.entries.ceiling.name).toBe("Campaign ceiling");
  expect(en.workspace.block.party).toBe("Party");
});

// The guard of the vocabulary also holds for the catalogs of the engine, generated here.
// The one exception is the exact label of the owner of a request that blocks, not the words inside a sentence.
const engineBad = {
  "pt-BR": {
    re: /\b(Workspaces?|workspaces?|Dono)\b/,
    label: "Dono: {{owner}}",
  },
  en: { re: /\b(Workspaces?|workspaces?|Owner)\b/, label: "Owner: {{owner}}" },
} as const;

function vocabularyViolations(
  texts: Record<string, string>,
  lang: keyof typeof engineBad,
): string[] {
  const { re, label } = engineBad[lang];
  return Object.entries(texts)
    .filter(([, text]) => text.trim() !== label && re.test(text.replace(/{{\w+}}/g, "")))
    .map(([key]) => key);
}

// CA-MSG-11
test("the texts of the engine keep the vocabulary in both languages", () => {
  expect(vocabularyViolations(messages["pt-BR"], "pt-BR")).toEqual([]);
  expect(vocabularyViolations(messages.en, "en")).toEqual([]);
});

// CA-MSG-11
test("the guard of the vocabulary names the key of a bad text and lets only the exact label through", () => {
  expect(
    vocabularyViolations(
      {
        "a.bad": "Dono da credencial",
        "a.label": "Dono: {{owner}}",
        "a.inside": "Veja Dono: {{owner}}",
        "a.ok": "Senhor da Guilda",
      },
      "pt-BR",
    ),
  ).toEqual(["a.bad", "a.inside"]);
  expect(
    vocabularyViolations(
      {
        "b.bad": "Owner of it",
        "b.label": "Owner: {{owner}}",
        "b.inside": "See Owner: {{owner}}",
        "b.ws": "the workspace",
      },
      "en",
    ),
  ).toEqual(["b.bad", "b.inside", "b.ws"]);
});
