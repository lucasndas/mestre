import { expect, setupCode, test } from "./support";

// The smoke of the e2e job (docs/qualidade/testes.md#smoke-do-painel): the real
// engine with the embedded panel, a browser, and the three steps a person takes
// first: the setup wizard, a login and the logout.
test("the Owner creates the Guild, signs out and signs in again", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveTitle("Mestre");
  // a Guild with no user opens the wizard
  await expect(page.getByRole("heading", { name: "Crie a sua Guilda" })).toBeVisible();
  await page.getByLabel("Código de configuração").fill(setupCode());
  await page.getByLabel("Seu nome").fill("Maria Souza");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha (mínimo 12 caracteres)").fill("uma senha bem longa");
  await page.getByLabel("Repita a senha").fill("uma senha bem longa");
  await page.getByLabel("PIN (6 números)").fill("123456");
  await page.getByLabel("Repita o PIN").fill("123456");
  await page.getByRole("button", { name: "Criar a Guilda" }).click();
  await expect(page.getByRole("heading", { name: "Sua Guilda está pronta" })).toBeVisible();

  // logout
  await page.getByRole("button", { name: "Menu da conta" }).click();
  await page.getByRole("menuitem", { name: "Sair" }).click();
  await expect(page.getByRole("heading", { name: "Entrar na Guilda" })).toBeVisible();

  // a wrong password is refused, the right one opens the panel
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("senha errada demais");
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("alert")).toHaveText("Usuário ou senha inválidos.");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("heading", { name: "Sua Guilda está pronta" })).toBeVisible();
});
