import { expect, test } from "@playwright/test";

// The smoke of the e2e job (docs/qualidade/testes.md#smoke-do-painel): the built bundle loads in a real browser.
test("the built panel loads and has the right title", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveTitle("Mestre");
  await expect(page.getByRole("heading", { name: "Mestre" })).toBeVisible();
});
