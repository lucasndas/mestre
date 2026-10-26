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

  // the terminal of a character whose agent is the fake one shows its replay: what the
  // agent printed before the browser opened the terminal, which the Citadel asked of the
  // machine and relayed, drawn by xterm.js at the fixed size of the PTY
  await page.goto("/terminal/e2e-dev");
  await expect(page.getByRole("heading", { name: "Terminal de Dev-1" })).toBeVisible();
  const screen = page.getByRole("region", { name: "Terminal de Dev-1" });
  await expect(screen.locator(".xterm-accessibility-tree")).toContainText("Claude Code (fake)");
  // the Owner may type: the keyboard is released by a button, and refused until then
  await expect(page.getByRole("button", { name: "Digitar" })).toBeVisible();
});

// The administration step of the smoke: the Owner registers a user (with the PIN, as
// the server demands) and sees the code of first access, which shows up once.
test("the Owner registers a user and sees the code of first access", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await page.getByRole("link", { name: "Usuários" }).click();
  await expect(page.getByRole("heading", { name: "Usuários", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: "Cadastrar usuário" }).click();
  await page.getByLabel("Nome", { exact: true }).fill("Ana Lima");
  await page.getByRole("button", { name: "Cadastrar usuário" }).click();
  await page.getByLabel("PIN").fill("123456");
  await page.getByRole("button", { name: "Cadastrar usuário" }).click();
  await expect(
    page.getByRole("heading", { name: "Código de primeiro acesso de Ana Lima" }),
  ).toBeVisible();
  await expect(page.getByTestId("shown-once")).toHaveText(/^[A-Z0-9]{8,}$/);
  await expect(page.getByText("Ana Lima").first()).toBeVisible();
  // the code is shown once: after it is written down, it is gone
  await page.getByRole("button", { name: "Já anotei" }).click();
  await expect(page.getByTestId("shown-once")).toHaveCount(0);
});
