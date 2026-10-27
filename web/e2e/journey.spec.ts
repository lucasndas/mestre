import { expect, test } from "./support";

// The journey of the first use (docs/painel/primeira-jornada.md, CA-JORN-12), on the Guild the
// smoke founded: the Guildmaster opens the Home and sees what the Guild has, asks the Grão-Mestre
// for something, decides what is asked of them and sees the table and the delivery. GitHub and the
// agents are doubles (tools/e2eserver), so the first two steps cannot be done here: the panel
// shows them as they are, with the way to do each one.
test("the Guildmaster walks the Home: first steps, tables, the request, the approval and the delivery", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByLabel("Nome de login").fill("maria");
  await page.getByLabel("Senha").fill("uma senha bem longa");
  await page.getByRole("button", { name: "Entrar" }).click();
  await expect(page.getByRole("heading", { name: "Início", level: 1 })).toBeVisible();

  // the first steps are derived from what the Guild has: the campaign exists, GitHub and the
  // account of the AI do not, and the second one waits for nothing
  const steps = page.getByRole("region", { name: "Primeiros passos" });
  await expect(steps).toBeVisible();
  const github = steps.getByRole("listitem").filter({ hasText: "Conectar o GitHub" });
  await expect(github.getByRole("link", { name: "Conectar o GitHub" })).toBeVisible();
  const campaign = steps.getByRole("listitem").filter({ hasText: "Criar a primeira campanha" });
  await expect(campaign.getByText("Feito")).toBeVisible();

  // what is happening: the table of the campaign, who works on it, and the delivery already made
  const now = page.getByRole("region", { name: "Acontecendo agora" });
  const table = now.getByRole("link", { name: /Abrir a mesa Login com Google/ });
  await expect(table).toBeVisible();
  await expect(now).toContainText("3 personagens (e2e)");
  const deliveries = page.getByRole("region", { name: "Entregas recentes" });
  await expect(deliveries).toContainText("Página de contato");
  await expect(deliveries).toContainText("PR #10 mesclado");
  await expect(page.getByRole("complementary", { name: "Saúde da Guilda" })).toBeVisible();

  // the first request, in the words of the person; the Grão-Mestre of the double raises an approval
  await page.getByRole("button", { name: "Falar com o Grão-Mestre" }).click();
  const chat = page.getByRole("complementary", { name: "Conversa com o Grão-Mestre" });
  await chat
    .getByLabel("Mensagem para o Grão-Mestre")
    .fill("preciso de mais orçamento para o login");
  await chat.getByRole("button", { name: "Enviar" }).click();
  await expect(chat.getByText("Mensagem enviada.")).toBeVisible();
  await chat.getByRole("button", { name: "Fechar a conversa" }).click();

  // the request waits in the Home, with a link that leads to it, and the PIN approves it
  await page.goto("/");
  const waiting = page.getByRole("region", { name: "Precisa de você" });
  await waiting
    .getByRole("link", { name: /Decidir o pedido/ })
    .first()
    .click();
  const card = page.getByRole("article", { name: /Pedido #\d+/ });
  await expect(card).toBeVisible();
  await card.getByRole("button", { name: "Aprovar agora" }).click();
  await card.getByLabel("PIN").fill("123456");
  await card.getByRole("button", { name: "Confirmar" }).click();
  await expect(card.getByText(/Você respondeu: Aprovado/)).toBeVisible();

  // answered, it leaves the Home; the table opens from there
  await page.goto("/");
  await expect(page.getByRole("region", { name: "Precisa de você" })).toHaveCount(0);
  await page
    .getByRole("region", { name: "Acontecendo agora" })
    .getByRole("link", { name: /Abrir a mesa Login com Google/ })
    .click();
  await expect(page.getByRole("region", { name: "Canvas da mesa mesa-e2e" })).toBeVisible();
});
