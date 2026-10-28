import type { Page } from "@playwright/test";
import { expect, test } from "./support";

// On a phone (390 px wide) no main screen scrolls sideways; the unit tests cannot measure a
// layout, so the real browser visits the screens and compares the page with the window.
// Part of CA-ADM-24
// Part of CA-CRESC-17
// Part of CA-JORN-11
// Part of CA-VIDA-10
// Part of CA-TUI-11
// Part of CA-CONV-18
test.use({ viewport: { width: 390, height: 844 } });

const overflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

async function noSidewaysScroll(page: Page, where: string) {
  await expect(page.getByRole("heading", { level: 1 }).first(), where).toBeVisible();
  await expect
    .poll(() => overflow(page), { message: `${where} scrolls sideways on a phone` })
    .toBeLessThanOrEqual(0);
}

test("the main screens fit in 390 px with no sideways scroll", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await noSidewaysScroll(page, "/");

  // the conversation with the Grão-Mestre takes the whole screen
  await page.getByRole("button", { name: "Abrir a conversa com o Grão-Mestre" }).click();
  await expect(
    page.getByRole("complementary", { name: "Conversa com o Grão-Mestre" }),
  ).toBeVisible();
  await expect.poll(() => overflow(page)).toBeLessThanOrEqual(0);
  await page.getByRole("button", { name: "Fechar a conversa" }).click();

  for (const path of [
    "/workspaces",
    "/workspaces/e2e",
    "/workspaces/e2e/mesas/mesa-e2e",
    "/workspaces/loja",
    "/aprovacoes",
    "/configuracoes",
    "/usuarios",
    "/guilda",
    "/decretos",
    "/classes",
    "/auditoria",
    "/sessoes",
  ]) {
    await page.goto(path);
    // still signed in: the login screen would pass the measure for nothing
    await expect(page.getByRole("heading", { name: "Entrar na Guilda" })).toHaveCount(0);
    await noSidewaysScroll(page, path);
  }
});
