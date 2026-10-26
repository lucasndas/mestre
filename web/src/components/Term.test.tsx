import { globSync, readFileSync } from "node:fs";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { glossary } from "../gen/glossary";
import { renderWithI18n } from "../test-utils";
import { Term } from "./Term";

// CA-CANVAS-02: the help of the "?" of a term is exactly the short help of the
// glossary table. gen/glossary.ts is the table at build time (a Go test proves the
// file is not stale), and a term with no row of its own does not type-check.
// CA-CANVAS-02
test("the help of Party is exactly the short help of the glossary", async () => {
  const party = glossary.Party["pt-BR"];
  expect(party.help).toBe("O time de personagens conectados desta mesa.");
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderWithI18n(<Term name="Party" />, "pt-BR");
  await user.click(screen.getByRole("button", { name: /Party/ }));
  const dialog = await screen.findByRole("dialog");
  expect(dialog.textContent).toContain(party.help);
  expect(dialog.textContent).toContain(party.real);
  expect(dialog.textContent).toContain("No mundo real");
});

// CA-CANVAS-02
test("every <Term name> of the panel is a row of the glossary", () => {
  const names = new Set(Object.keys(glossary));
  const used: string[] = [];
  for (const file of globSync("src/**/*.tsx")) {
    if (file.endsWith(".test.tsx")) continue;
    for (const m of readFileSync(file, "utf8").matchAll(/<Term\s+name="([^"]+)"/g))
      used.push(m[1] ?? "");
  }
  expect(used.length).toBeGreaterThan(0);
  for (const u of used) expect(names.has(u), `term "${u}" has no row in the glossary`).toBe(true);
});

test("the help is in English when the panel is in English", async () => {
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderWithI18n(<Term name="Cidadela" />, "en");
  await user.click(screen.getByRole("button", { name: /Citadel/ }));
  const dialog = await screen.findByRole("dialog");
  expect(dialog.textContent).toContain(glossary.Cidadela.en.help);
  expect(dialog.textContent).toContain("In the real world");
  render(<span />);
});
