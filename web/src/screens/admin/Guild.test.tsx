import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { fakeServer, fill, ME, renderApp } from "../../test-utils";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const OWNER_CAPS = ["manage_machines", "guild_settings", "status"];
const base = {
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/me": () => ({ body: { ...ME, capabilities: OWNER_CAPS } }),
  "GET /api/transfer": () => ({ body: {} }),
};

const sheet = {
  os: "linux",
  gui: true,
  providers: ["claude-code: padrão"],
  browser: "Chrome 140",
  operator: "off",
  cpu_cores: 8,
  mem_bytes: 17179869184,
  gpus: [],
  transcribes: true,
};
const machine = (o: Record<string, unknown>) => ({
  id: "m1",
  name: "casa",
  citadel: true,
  self: true,
  online: true,
  outside_network: false,
  sheet,
  excluded: false,
  load: { disk_free_bytes: 53687091200, running: 1 },
  agents: [{ id: "c1", name: "Dev-1", workspace: "loja", table: "login", state: "busy" }],
  priority: 0,
  ...o,
});
const tower = machine({
  id: "m2",
  name: "torre",
  citadel: false,
  self: false,
  priority: 3,
  agents: [],
  protocol: 1,
  replica: { state: "streaming", behind: 0, lag_ms: 0 },
});
const view = (o: Record<string, unknown>) => ({
  epoch: 4,
  timezone: "UTC",
  single: false,
  machines: [machine({}), tower],
  pending: [],
  ...o,
});
const owner = { "GET /api/guild": () => ({ body: view({}) }) };

// CA-ADM-08
test("the Guild screen shows the band of the single-machine mode while the Citadel is alone, and the epoch", async () => {
  fakeServer({
    ...base,
    "GET /api/guild": () => ({ body: view({ single: true, machines: [machine({})] }) }),
  });
  renderApp("/guilda");
  expect(await screen.findByRole("complementary", { name: "Modo máquina única" })).toBeTruthy();
  expect(screen.getByText("Época 4")).toBeTruthy();
  expect(screen.getByRole("link", { name: /Dev-1/ }).getAttribute("href")).toBe(
    "/workspaces/loja/mesas/login",
  );
});

test("with a Tower in the Guild the band is gone and the row shows the replica and the load", async () => {
  fakeServer({ ...base, ...owner });
  renderApp("/guilda");
  const row = await screen.findByRole("listitem", { name: "torre" });
  expect(screen.queryByRole("complementary", { name: "Modo máquina única" })).toBeNull();
  expect(within(row).getByText("Torre")).toBeTruthy();
  expect(within(row).getByText(/em dia/)).toBeTruthy();
  expect(within(row).getByText("protocolo 1")).toBeTruthy();
  expect(within(row).getByText(/Disco livre: 50 GB/)).toBeTruthy();
});

// Part of CA-TRAVA-07 (the panel shows "Telegram parado" and "GitHub fora")
test("the row of the Citadel says GitHub fora and Telegram parado when the engine does", async () => {
  fakeServer({
    ...base,
    "GET /api/guild": () => ({
      body: view({ machines: [machine({ github_down: true, telegram_stopped: true })] }),
    }),
  });
  renderApp("/guilda");
  expect(await screen.findByText("GitHub fora")).toBeTruthy();
  expect(screen.getByText("Telegram parado")).toBeTruthy();
});

// Part of CA-ADM-10 (the screen): the exclusion goes with the PIN in its own request
test("the Owner excludes a Tower with the PIN and the row follows the new view", async () => {
  let excluded = false;
  const { calls } = fakeServer({
    ...base,
    "GET /api/guild": () => ({
      body: view({ machines: [machine({}), { ...tower, excluded }] }),
    }),
    "POST /api/guild/machines/m2/exclusion": () => {
      excluded = true;
      return { status: 204 };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  await user.click(await screen.findByRole("button", { name: "Excluir torre" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Excluir torre" }));
  expect(await screen.findByText("torre agora está excluída.")).toBeTruthy();
  const post = calls.find((c) => c.method === "POST" && c.path.endsWith("/exclusion"));
  expect(post?.headers["X-Mestre-Pin"]).toBe("123456");
  expect(post?.body).toEqual({ excluded: true });
  expect(await screen.findByRole("button", { name: "Tirar a exclusão de torre" })).toBeTruthy();
  // an excluded machine is not offered the Citadel
  expect(
    (screen.getByRole("button", { name: "Passar a Cidadela a torre" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  expect(screen.getByText("Máquina excluída não vira Cidadela.")).toBeTruthy();
});

// Part of CA-ADM-11 (the screen): the refusal of the Citadel is told with its reason
test("passing the Citadel to a machine the server refuses shows the reason", async () => {
  fakeServer({
    ...base,
    ...owner,
    "POST /api/guild/machines/m2/pass-citadel": () => ({
      status: 409,
      body: {
        error: { code: "refused", message: "x", params: { reason: "outside_local_network" } },
      },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  await user.click(await screen.findByRole("button", { name: "Passar a Cidadela a torre" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Passar a Cidadela a torre" }));
  expect((await screen.findByRole("alert")).textContent).toContain("fora da rede local");
});

test("removing a machine warns how many and which agents run there before the PIN, and confirms in the request", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/guild": () => ({ body: view({ machines: [machine({}), { ...tower, agents: [] }] }) }),
    "GET /api/guild/machines/m2/removal": () => ({
      body: {
        machine: "m2",
        name: "torre",
        online: false,
        agents: [
          { id: "c9", name: "Revisor", workspace: "loja", table: "login", state: "idle" },
          { id: "c8", name: "Dev-2", workspace: "loja", state: "busy" },
        ],
      },
    }),
    "POST /api/guild/machines/m2/remove": () => ({ body: { notified: false } }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  await user.click(await screen.findByRole("button", { name: "Remover torre" }));
  const warning = await screen.findByRole("group", { name: "Remover torre?" });
  expect(within(warning).getByText(/Rodam nela 2 agente\(s\)/)).toBeTruthy();
  expect(within(warning).getByText("Revisor")).toBeTruthy();
  expect(within(warning).getByText(/fora do ar/)).toBeTruthy();
  await user.click(within(warning).getByRole("button", { name: "Remover da Guilda" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(within(warning).getByRole("button", { name: "Remover da Guilda" }));
  expect(await screen.findByText(/troque os segredos/)).toBeTruthy();
  const post = calls.find((c) => c.path === "/api/guild/machines/m2/remove");
  expect(post?.body).toEqual({ confirm: true });
  expect(post?.headers["X-Mestre-Pin"]).toBe("123456");
});

test("a request to enter shows the code to compare and is accepted with the PIN", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/guild": () => ({
      body: view({
        pending: [
          { id: "j1", name: "notebook", code: "483 920", created_at: "2026-10-26T12:00:00Z" },
        ],
      }),
    }),
    "POST /api/guild/joins/j1/decide": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  expect(await screen.findByText("483 920")).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Aceitar" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Aceitar" }));
  await waitFor(() =>
    expect(calls.some((c) => c.path === "/api/guild/joins/j1/decide")).toBe(true),
  );
  expect(calls.find((c) => c.path.endsWith("/decide"))?.body).toEqual({
    accept: true,
    excluded: false,
  });
});

test("the Owner changes the priority and the time zone of the Guild with the PIN", async () => {
  const { calls } = fakeServer({
    ...base,
    ...owner,
    "PUT /api/guild/machines/m2/priority": () => ({ status: 204 }),
    "PUT /api/guild/timezone": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  const field = await screen.findByLabelText("Prioridade de torre");
  await user.clear(field);
  await fill(user, field, "9");
  await user.click(screen.getByRole("button", { name: "Salvar prioridade" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Salvar prioridade" }));
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/priority"))).toBe(true));
  expect(calls.find((c) => c.path.endsWith("/priority"))?.body).toEqual({ priority: 9 });

  await user.selectOptions(screen.getByLabelText("Fuso horário"), "America/Sao_Paulo");
  await user.click(screen.getByRole("button", { name: "Salvar o fuso" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Salvar o fuso" }));
  expect(await screen.findByText("O fuso da Guilda agora é America/Sao_Paulo.")).toBeTruthy();
  expect(calls.find((c) => c.path === "/api/guild/timezone")?.body).toEqual({
    timezone: "America/Sao_Paulo",
  });
});

test("a level that cannot manage machines does not see the entry in the nav and gets the level required", async () => {
  fakeServer({
    ...base,
    "GET /api/me": () => ({
      body: { ...ME, user: { ...ME.user, level: "administrator" }, capabilities: ["status"] },
    }),
    "GET /api/guild": () => ({
      status: 403,
      body: {
        error: {
          code: "forbidden_level",
          message: "x",
          params: { need: "owner", have: "administrator", capability: "manage_machines" },
        },
      },
    }),
  });
  renderApp("/guilda");
  expect((await screen.findByRole("alert")).textContent).toBe(
    "Esta ação exige o nível Dono; o seu é Administrador.",
  );
  expect(screen.queryByRole("link", { name: "Guilda" })).toBeNull();
});
