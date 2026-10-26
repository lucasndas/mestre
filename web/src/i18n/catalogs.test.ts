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
