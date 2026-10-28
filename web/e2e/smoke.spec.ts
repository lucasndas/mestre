import type { Page } from "@playwright/test";
import { expect, setupCode, test } from "./support";

// A screen of Configurações takes two clicks: the item of the menu and the entry of the index.
async function openSetting(page: Page, name: RegExp) {
  await page
    .getByRole("navigation", { name: "Navegação principal" })
    .getByRole("link", { name: "Configurações" })
    .click();
  await page.getByRole("main").getByRole("link", { name }).click();
}

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
  await expect(page.getByRole("heading", { name: "Início" })).toBeVisible();

  // the request the throwaway Guild has waiting (tools/e2eserver/seed.go): the counter says
  // so, the central shows it, and it is approved with the PIN
  await expect(page.getByRole("link", { name: "Aprovações pendentes: 3" })).toBeVisible();
  await page.getByRole("link", { name: "Aprovações pendentes: 3" }).click();
  const card = page.getByRole("article", { name: "Pedido #1" });
  await expect(card).toBeVisible();
  await card.getByRole("button", { name: "Aprovar agora" }).click();
  await card.getByLabel("PIN").fill("123456");
  await card.getByRole("button", { name: "Confirmar" }).click();
  await expect(card.getByText(/Você respondeu: Aprovado/)).toBeVisible();
  await expect(page.getByRole("link", { name: "Aprovações pendentes: 2" })).toBeVisible();

  // the conversation with the Grão-Mestre (a function in this Guild, tools/e2eserver/chat.go): the
  // message goes to its inbox, it answers at once through the channel of the panel, and the answer
  // shows in Markdown in the side panel
  await page.getByRole("button", { name: "Abrir a conversa com o Grão-Mestre" }).click();
  const chat = page.getByRole("complementary", { name: "Conversa com o Grão-Mestre" });
  await expect(chat.getByText("Pronto para ouvir")).toBeVisible();
  await chat.getByLabel("Mensagem para o Grão-Mestre").fill("abra a mesa do login");
  await chat.getByRole("button", { name: "Enviar" }).click();
  await expect(chat.getByText("Mensagem enviada.")).toBeVisible();
  await expect(chat.locator("strong", { hasText: "abra a mesa do login" })).toBeVisible();
  await chat.getByRole("button", { name: "Fechar a conversa" }).click();

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
  await expect(page.getByRole("heading", { name: "Início" })).toBeVisible();

  // the terminal of a character whose agent is the fake one shows its replay: what the
  // agent printed before the browser opened the terminal, which the Citadel asked of the
  // machine and relayed, drawn by xterm.js at the fixed size of the PTY
  await page.goto("/terminal/e2e-dev");
  await expect(page.getByRole("heading", { name: "Terminal de Dev-1" })).toBeVisible();
  const screen = page.getByRole("region", { name: "Terminal de Dev-1" });
  await expect(screen.locator(".xterm-accessibility-tree")).toContainText("Claude Code (fake)");
  // the Owner may type: the keyboard is released by a button, and refused until then
  await expect(page.getByRole("button", { name: "Digitar" })).toBeEnabled();
  // the window of the terminal: the identity bar and the mode in the status line
  await expect(page.getByRole("toolbar")).toContainText("Dev-1");
  await expect(page.getByRole("button", { name: "Ajuda sobre Leitura" })).toBeVisible();
});

// The administration step of the smoke (CA-CRESC-18): the Owner invites a person (with the PIN,
// as the server demands) and the invite comes in three parts; the person, in another browser,
// does the first access with the code, sees the welcome of their level, and the same code does
// not serve a second time.
// Part of CA-CRESC-18 (the criterion is *completa*: this e2e is the whole journey)
test("the Owner invites a person, who does the first access once with the code", async ({
  page,
  browser,
  baseURL,
}) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await openSetting(page, /^Usuários/);
  await expect(page.getByRole("heading", { name: "Usuários", level: 1 })).toBeVisible();
  await page.getByRole("link", { name: "Convidar pessoa" }).first().click();
  await expect(page.getByRole("heading", { name: "Convidar pessoa", level: 1 })).toBeVisible();
  await page.getByLabel("Nome da pessoa").fill("Ana Lima");
  await page.getByRole("radiogroup").locator("label", { hasText: "Observador" }).click();
  await page.getByRole("button", { name: "Criar convite" }).click();
  await page.getByRole("textbox", { name: "PIN" }).fill("123456");
  await page.getByRole("button", { name: "Criar convite" }).click();

  // the three parts: the check code is computed from /ca.crt, the secret waits for the word
  const p2 = page.getByRole("region", { name: "Parte 2 · O código de conferência do certificado" });
  await expect(p2.locator("pre")).toHaveText(/^([0-9a-f]{8} ){7}[0-9a-f]{8}$/);
  await expect(page.getByRole("region", { name: "Parte 1 · As instruções" })).toBeVisible();
  await expect(page.getByTestId("shown-once")).toHaveCount(0);
  await page.locator("label", { hasText: "conferiu o código" }).click();
  const code = (await page.getByTestId("shown-once").textContent()) ?? "";
  expect(code).toMatch(/^[A-Z0-9]{8,}$/);
  await expect(page.getByRole("heading", { name: "Convite de Ana Lima", level: 1 })).toBeVisible();
  // the code is shown once: after it is written down, the page goes back to the list
  await page.getByRole("button", { name: "Anotei, pode sumir" }).click();
  await expect(page.getByRole("heading", { name: "Usuários", level: 1 })).toBeVisible();
  await expect(page.getByTestId("shown-once")).toHaveCount(0);
  await expect(page.getByText("Ainda não entrou", { exact: true })).toBeVisible();

  // the person, in another browser: the address carries nothing, the code is typed
  const guest = await browser.newContext({ baseURL, ignoreHTTPSErrors: true, locale: "pt-BR" });
  const ana = await guest.newPage();
  await ana.goto("/primeiro-acesso");
  await ana.getByLabel("Código de primeiro acesso").fill(code);
  await ana.getByLabel("Escolha o nome de login").fill("ana");
  await ana.getByLabel("Senha (mínimo 12 caracteres)").fill("uma senha bem longa");
  await ana.getByLabel("Repita a senha").fill("uma senha bem longa");
  await ana.getByLabel("PIN (6 a 12 números)").fill("654321");
  await ana.getByLabel("Repita o PIN").fill("654321");
  await ana.getByRole("button", { name: "Criar meu acesso" }).click();
  await expect(ana.getByRole("heading", { name: "Início" })).toBeVisible();
  await expect(ana.getByRole("region", { name: "Você entrou como Observador" })).toBeVisible();
  await guest.close();

  // the same code does not serve twice
  const again = await browser.newContext({ baseURL, ignoreHTTPSErrors: true, locale: "pt-BR" });
  const other = await again.newPage();
  await other.goto("/primeiro-acesso");
  await other.getByLabel("Código de primeiro acesso").fill(code);
  await other.getByLabel("Escolha o nome de login").fill("ana2");
  await other.getByLabel("Senha (mínimo 12 caracteres)").fill("uma senha bem longa");
  await other.getByLabel("Repita a senha").fill("uma senha bem longa");
  await other.getByLabel("PIN (6 a 12 números)").fill("654321");
  await other.getByLabel("Repita o PIN").fill("654321");
  await other.getByRole("button", { name: "Criar meu acesso" }).click();
  await expect(other.getByRole("alert")).toContainText("Esse código não vale mais");
  await again.close();
});

// The canvas step of the smoke: the table of the engine double is drawn with its
// characters, the machine badge, the mission arrow with its number, the wait and the
// scroll with its Markdown, and the terminal of the character whose agent is the fake one.
test("the canvas of a table draws its nodes and the terminal of a character", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("heading", { name: "Início" })).toBeVisible();
  await page.goto("/workspaces/e2e/mesas/mesa-e2e");
  const canvas = page.getByRole("region", { name: "Canvas da mesa mesa-e2e" });
  await expect(canvas).toBeVisible();
  // the header of the table: the title with the identifier, and the two parts it has
  await expect(page.getByRole("button", { name: "Conversar sobre esta mesa" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Quadro" })).toHaveAttribute("aria-selected", "true");
  const dev = canvas.getByRole("article", { name: "Personagem Dev-1" });
  await expect(dev).toBeVisible();
  await expect(dev.getByText("Máquina e2e")).toBeVisible();
  await expect(dev.getByRole("status")).toHaveText("Trabalhando");
  await expect(canvas.getByRole("article", { name: "Personagem Revisor" })).toBeAttached();
  await expect(canvas.getByRole("article", { name: "Pergaminho Plano" })).toBeAttached();
  // the terminal of Dev-1 is the real one, over the socket
  await expect(dev.getByRole("region", { name: "Terminal de Dev-1" })).toBeVisible();
  await expect(dev.locator(".xterm")).toBeVisible();
  // the lines: the number of the step of the mission (the arrow into step 2, highlighted because step 1 is under way) and the reason of the wait
  await expect(canvas.locator('[data-edge-kind="step"]')).toHaveText("2");
  await expect(canvas.locator('[data-edge-kind="step"]')).toHaveAttribute("data-current", "true");
  await expect(canvas.locator('[data-edge-kind="wait"]')).toHaveText("Esperando Dev-1");
  // an action goes to the engine and the canvas shows its answer: pin Dev-1
  await dev.getByRole("button", { name: "Fixar Dev-1" }).click();
  await expect(dev.getByRole("button", { name: "Desafixar Dev-1" })).toBeVisible();
  // the terminal of Dev-1 is also the Terminal tab of the table, in the same window
  await page.getByRole("tab", { name: "Terminal" }).click();
  await expect(page).toHaveURL(/aba=terminal/);
  await expect(page.getByRole("region", { name: "Terminal de Dev-1" })).toBeVisible();
});

// The workspace view step of the smoke: the Grand Master of the double is the center, with its
// terminal over the socket, the table is a block around it, and a click on the block
// opens the canvas of the table.
// Part of CA-VWS-03 (the screen test is in web/src/workspace/workspace.test.tsx)
test("the workspace view shows the Grand Master and a table block, and a click opens its canvas", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("heading", { name: "Início" })).toBeVisible();
  // Campanhas, then Abrir on the card, leads to the view of the workspace
  await page
    .getByRole("navigation", { name: "Navegação principal" })
    .getByRole("link", { name: "Campanhas" })
    .click();
  await page
    .getByRole("article", { name: "e2e" })
    .getByRole("link", { name: "Abrir a campanha e2e" })
    .click();
  await expect(page.getByRole("heading", { name: "Campanha e2e", level: 1 })).toBeVisible();
  const canvas = page.getByRole("region", { name: "Canvas da campanha e2e" });
  const gm = canvas.getByRole("article", { name: "Grão-Mestre Grão-Mestre" });
  await expect(gm).toBeVisible();
  await expect(gm.locator(".xterm")).toBeVisible();
  const block = canvas.getByRole("article", { name: "Mesa mesa-e2e" });
  await expect(block).toBeVisible();
  await expect(block.getByText("Login com Google")).toBeVisible();
  await expect(block.getByText("Trabalhando")).toBeVisible();
  // the criteria are a technical detail, folded
  await block.getByText("Detalhes técnicos").click();
  await expect(block.getByText("Critérios 2 de 3 (1 verificado)")).toBeVisible();
  await block.getByText("epico/7-login").click();
  await expect(page).toHaveURL(/\/workspaces\/e2e\/mesas\/mesa-e2e$/);
  await expect(page.getByRole("region", { name: "Canvas da mesa mesa-e2e" })).toBeVisible();
});

// The rules step of the smoke: the Owner opens the decrees screen, makes a rule (a
// proposal first, then the click) and revokes it. The engine is the real one.
test("the Owner makes a decree and revokes it", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await openSetting(page, /^Decretos/);
  await expect(page.getByRole("heading", { name: "Decretos", level: 1 })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Para que serve um decreto?" })).toBeVisible();

  await page.getByRole("button", { name: "Criar decreto" }).click();
  await page.getByLabel("Nome", { exact: true }).fill("dev");
  await page.getByLabel("Padrão").fill("npm test*");
  await page
    .getByLabel("A ordem, com as suas palavras")
    .fill("dev pode rodar npm test sem perguntar");
  await page.getByRole("button", { name: "Ver o que o Mestre entendeu" }).click();
  // a proposal is not a decree: nothing is in force before the click
  await expect(page.getByText("Isto ainda não é um decreto")).toBeVisible();
  const confirm = page.getByRole("button", { name: "Confirmar decreto" });
  await confirm.click();
  await page.getByLabel("PIN").fill("123456");
  await page.getByRole("button", { name: "Confirmar decreto" }).click();
  await expect(page.getByText("Decreto feito.")).toBeVisible();
  const card = page.getByRole("article", { name: "Decreto 1" });
  await expect(card).toContainText("dev pode rodar npm test sem perguntar");

  await card.getByRole("button", { name: "Revogar" }).click();
  await card.getByRole("button", { name: "Sim, revogar" }).click();
  await expect(page.getByText("Decreto 1 revogado.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Para que serve um decreto?" })).toBeVisible();
});

// The classes and presets step of the smoke: the approved class of the throwaway Guild is
// listed, and the sheet "Dev Claude" goes out as a YAML file and comes back, replacing itself.
test("the Owner sees the classes and exports and imports a sheet", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await openSetting(page, /^Classes/);
  await expect(page.getByRole("heading", { name: "Classes", level: 1 })).toBeVisible();
  await expect(page.getByText("revisor", { exact: true }).first()).toBeVisible();

  await openSetting(page, /^Bases e fichas/);
  await expect(page.getByRole("heading", { name: "Bases e fichas", level: 1 })).toBeVisible();
  // the checkbox is drawn by its label: the click goes to the label
  await page.getByRole("group", { name: "Fichas a exportar" }).getByText("Dev Claude").click();
  const [file] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Exportar" }).click(),
  ]);
  await expect(page.getByText("Arquivo exportado.")).toBeVisible();
  await page.getByLabel("Arquivo para importar").setInputFiles(await file.path());
  await page.getByRole("button", { name: "Conferir o arquivo" }).click();
  await page.getByLabel("Ficha Dev Claude").selectOption({ label: "Substituir" });
  await page.getByRole("button", { name: "Importar", exact: true }).click();
  await page.getByLabel("PIN").fill("123456");
  await page.getByRole("button", { name: "Importar", exact: true }).click();
  await expect(page.getByText(/Importação concluída/)).toContainText("substituídas: 1");
});

// The Guild step of the smoke: the real Citadel is alone in its throwaway Guild, so the
// screen shows the strip of the single machine, the card to add a second one, its own row and
// the epoch.
test("the Guild screen shows the single-machine band and the Citadel", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await openSetting(page, /^Guilda/);
  await expect(page.getByRole("heading", { name: "Guilda", level: 1 })).toBeVisible();
  await expect(page.getByText("1 máquina, e é a Cidadela")).toBeVisible();
  await expect(page.getByRole("region", { name: "Adicionar uma segunda máquina" })).toBeVisible();
  await expect(page.getByText(/^Época \d+$/)).toBeVisible();
  const rows = page.getByRole("list", { name: "Máquinas" }).getByRole("listitem");
  await expect(rows).toHaveCount(1);
  await expect(rows.first().getByText("Cidadela", { exact: true })).toBeVisible();
  await expect(rows.first().getByText("esta máquina")).toBeVisible();
});

// The workspaces step of the smoke: the list shows the workspace of the double of the
// engine, and an Administrator-level Owner confirms the proposal of the verification
// commands (the confirmation is the version in force).
test("the workspaces list shows a workspace and the Owner confirms its verification commands", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await page
    .getByRole("navigation", { name: "Navegação principal" })
    .getByRole("link", { name: "Campanhas" })
    .click();
  await expect(page.getByRole("heading", { name: "Campanhas", level: 1 })).toBeVisible();
  const card = page.getByRole("article", { name: "e2e" });
  await expect(card.getByText("acme/e2e")).toBeVisible();
  await expect(card.getByText("Ativa", { exact: true })).toBeVisible();
  await card.getByRole("button", { name: "Comandos de verificação" }).click();
  await expect(card.getByText("Nenhuma versão confirmada")).toBeVisible();
  await card.getByRole("button", { name: "Confirmar estes comandos" }).click();
  await expect(card.getByText("Versão confirmada. Ela já vale.")).toBeVisible();
  await expect(card.getByText(/Em vigor: confirmada por Maria Souza/)).toBeVisible();
});
