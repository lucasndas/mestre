import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { groupHex, toHex } from "../../lib/caFingerprint";
import { fakeServer, fill, ME, renderApp } from "../../test-utils";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  localStorage.clear();
});

const OWNER_CAPS = [
  "manage_users",
  "bases_vault_policy",
  "activate_classes",
  "connect_own_github",
  "status",
];
const base = {
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/me": () => ({ body: { ...ME, capabilities: OWNER_CAPS } }),
  "GET /api/transfer": () => ({ body: {} }),
};
const row = (o: Record<string, unknown>) => ({
  id: "u2",
  name: "Bruno Dias",
  login: "bruno",
  level: "approver",
  level_name: "Aprovador",
  state: "active",
  workspaces: ["loja"],
  has_pin: true,
  telegram: false,
  github_login: "",
  github: { state: "", login: "" },
  created_at: "2026-10-20T12:00:00Z",
  ...o,
});
const owner = row({
  id: "u1",
  name: "Maria Souza",
  login: "maria",
  level: "owner",
  workspaces: [],
});
const CA = { ca: "bytes of the certificate" };

async function expectedCheckCode(): Promise<string> {
  const bytes = new TextEncoder().encode(JSON.stringify(CA));
  return toHex(await crypto.subtle.digest("SHA-256", bytes));
}

const invited = {
  ...base,
  "GET /api/users": () => ({ body: { users: [owner], workspaces: ["loja"] } }),
  "GET /ca.crt": () => ({ body: CA }),
  "POST /api/users": () => ({
    status: 201,
    body: {
      user: row({ id: "u3", name: "Ana Lima", login: "", level: "observer" }),
      code: { code: "ABCD2345", kind: "first", expires_at: "2026-10-27T12:30:00Z" },
    },
  }),
};

async function invite(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    (await screen.findAllByRole("button", { name: "Convidar pessoa" }))[0] as HTMLElement,
  );
  await fill(user, screen.getByLabelText("Nome"), "Ana Lima");
  await user.click(screen.getByRole("button", { name: "Criar convite" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Criar convite" }));
}

// CA-CRESC-01
test("choosing a level says what the person will and will not be able to do", async () => {
  fakeServer(invited);
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  await user.click(
    (await screen.findAllByRole("button", { name: "Convidar pessoa" }))[0] as HTMLElement,
  );
  await user.click(screen.getByRole("radio", { name: /Observador/ }));
  expect(screen.getByText(/Poderá acompanhar as campanhas/)).toBeTruthy();
  expect(screen.getByText(/Não poderá conversar com o Grão-Mestre/)).toBeTruthy();
  await user.click(screen.getByRole("radio", { name: /Administrador/ }));
  expect(screen.getByText(/Poderá criar campanhas/)).toBeTruthy();
  expect(screen.queryByLabelText("PIN")).toBeNull();
});

// CA-CRESC-01
test("the campaigns that were ticked go in the invite", async () => {
  const { calls } = fakeServer(invited);
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  await user.click(
    (await screen.findAllByRole("button", { name: "Convidar pessoa" }))[0] as HTMLElement,
  );
  await fill(user, screen.getByLabelText("Nome"), "Ana Lima");
  await user.click(screen.getByRole("checkbox", { name: "loja" }));
  await user.click(screen.getByRole("button", { name: "Criar convite" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Criar convite" }));
  await screen.findByRole("region", { name: "1. Instruções" });
  expect(calls.find((c) => c.method === "POST" && c.path === "/api/users")?.body).toMatchObject({
    name: "Ana Lima",
    workspaces: ["loja"],
  });
});

// CA-CRESC-02 and CA-CRESC-03
test("the invite has three parts with their own copy buttons: no code in the instructions, the check code computed from the certificate, the secret only after the confirmation", async () => {
  fakeServer(invited);
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  await invite(user);
  const check = groupHex(await expectedCheckCode());
  const p1 = await screen.findByRole("region", { name: "1. Instruções" });
  const p2 = screen.getByRole("region", { name: "2. Código de conferência do certificado" });
  const p3 = screen.getByRole("region", { name: "3. Código de primeiro acesso" });
  expect(p1.textContent).not.toContain("ABCD2345");
  expect(p1.textContent).not.toContain(check);
  expect(p1.textContent).not.toContain(check.replaceAll(" ", ""));
  expect(p2.textContent).toContain(check);
  expect(p1.textContent).toMatch(/NÃO instale/);
  expect(within(p1).getByRole("button", { name: /Copiar: 1\. Instruções/ })).toBeTruthy();
  expect(within(p2).getByRole("button", { name: /Copiar: 2\./ })).toBeTruthy();
  // the secret waits for the word of the person: the checkbox is what shows it
  expect(within(p3).queryByTestId("shown-once")).toBeNull();
  expect(within(p3).getByRole("checkbox", { name: /conferiu o código/ })).toBeTruthy();
  await user.click(within(p3).getByRole("checkbox", { name: /conferiu o código/ }));
  expect(within(p3).getByTestId("shown-once").textContent).toBe("ABCD2345");
  // nowhere is there a link that carries the code
  expect(document.body.innerHTML).not.toMatch(/codigo=/);
});

test("without a check code computed from the certificate the invite is not handed out", async () => {
  fakeServer({
    ...invited,
    "GET /ca.crt": () => ({ status: 500, body: {} }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  await invite(user);
  expect((await screen.findByRole("alert")).textContent).toContain("não entrego o convite");
  expect(screen.queryByRole("region", { name: "1. Instruções" })).toBeNull();
  expect(screen.queryByTestId("shown-once")).toBeNull();
});

// CA-CRESC-05
test("SHA-256 and CA appear only under Detalhes técnicos in what the person reads", async () => {
  fakeServer(invited);
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  await invite(user);
  const p1 = await screen.findByRole("region", { name: "1. Instruções" });
  const pre = p1.querySelector("pre") as HTMLElement;
  expect(pre.textContent).not.toMatch(/SHA-256|\bCA\b/);
  expect(pre.textContent).toMatch(/NÃO instale e me avise/);
  const details = p1.querySelector("details") as HTMLElement;
  expect(within(details).getByText("Detalhes técnicos")).toBeTruthy();
  expect(details.textContent).toMatch(/SHA-256/);
  expect(details.textContent).toMatch(/sha256sum/);
});

// CA-CRESC-07
test("a person who has not logged in yet shows as such, Gerenciar groups its actions with their effect, and a failed read offers to try again", async () => {
  let fail = true;
  fakeServer({
    ...base,
    "GET /api/users": () =>
      fail
        ? { status: 500, body: { error: { code: "internal", message: "boom" } } }
        : {
            body: {
              users: [owner, row({ id: "u3", name: "Ana Lima", login: "", workspaces: [] })],
              workspaces: ["loja"],
            },
          },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  expect((await screen.findByRole("alert")).textContent).toContain("Não foi possível carregar");
  fail = false;
  await user.click(screen.getByRole("button", { name: "Tentar de novo" }));
  expect(await screen.findByText("Ainda não entrou")).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Gerenciar Ana Lima" }));
  expect(screen.getByRole("heading", { name: "Códigos" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Acesso" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Posto de Senhor da Guilda" })).toBeTruthy();
  expect(screen.getByText(/o código anterior deixa de valer/)).toBeTruthy();
  expect(screen.getByText(/encerra as sessões e invalida o que a pessoa aprovou/)).toBeTruthy();
});

const asking = {
  from_id: "u1",
  from_name: "Maria Souza",
  to_id: "u2",
  to_name: "Bruno Dias",
  expires_at: "2026-10-28T12:00:00Z",
  to_me: false,
};

// CA-CRESC-15
test("a person without PIN cannot receive the post, and the reason is on the screen", async () => {
  fakeServer({
    ...base,
    "GET /api/users": () => ({
      body: {
        users: [owner, row({ id: "u2", name: "Bruno Dias", has_pin: false })],
        workspaces: [],
      },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  await user.click(await screen.findByRole("button", { name: "Gerenciar Bruno Dias" }));
  expect(
    (
      screen.getByRole("button", {
        name: "Passar o posto de Senhor da Guilda",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(screen.getByText(/Bruno Dias ainda não tem/)).toBeTruthy();
});

test("with the request made the screen shows the wait and cancelling it asks the PIN", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/users": () => ({
      body: {
        users: [owner, row({ id: "u2", name: "Bruno Dias" })],
        workspaces: [],
        transfer: asking,
      },
    }),
    "DELETE /api/transfer": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  expect(await screen.findByText(/espera a aceitação até/)).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Cancelar o pedido" }));
  expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Cancelar o pedido" }));
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  expect(calls.find((c) => c.method === "DELETE")?.headers["X-Mestre-Pin"]).toBe("123456");
});

// CA-CRESC-06
test("the welcome card of an Approver lists what the level allows and the campaigns, and an Observer is not told they can chat or approve", async () => {
  const as = (level: string) => ({
    ...base,
    "GET /api/me": () => ({
      body: {
        ...ME,
        user: { ...ME.user, id: "u9", name: "Ana Lima", level, workspaces: ["loja"] },
        capabilities: ["status"],
      },
    }),
  });
  fakeServer(as("approver"));
  const { unmount } = renderApp("/");
  const card = await screen.findByRole("region", { name: "Você entrou como Aprovador" });
  expect(card.textContent).toContain("decidir aprovações");
  expect(card.textContent).toContain("loja");
  expect(card.textContent).toContain("pareie o seu Telegram");
  unmount();
  fakeServer(as("observer"));
  renderApp("/");
  const obs = await screen.findByRole("region", { name: "Você entrou como Observador" });
  expect(within(obs).getByText("Acompanhar campanhas, mesas e o canvas.")).toBeTruthy();
  expect(
    within(obs).getByText("Conversar com o Grão-Mestre, decidir aprovações e tudo que muda algo."),
  ).toBeTruthy();
});

// CA-CRESC-17
test("on the phone the blocks of command scroll on their own and every control is tall enough", async () => {
  fakeServer(invited);
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  await invite(user);
  const p1 = await screen.findByRole("region", { name: "1. Instruções" });
  for (const pre of document.querySelectorAll("pre")) {
    expect(pre.className).toMatch(/overflow-x-auto/);
    expect(pre.className).toMatch(/max-w-full/);
  }
  for (const b of within(p1).getAllByRole("button")) expect(b.className).toMatch(/min-h-11/);
  for (const s of document.querySelectorAll("summary")) expect(s.className).toMatch(/min-h-11/);
  expect(document.body.innerHTML).not.toMatch(/\bw-\[\d+px\]|\bmin-w-\[\d+px\]/);
});
