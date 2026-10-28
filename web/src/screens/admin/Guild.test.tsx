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

// CA-ADM-08, CA-CRESC-08
test("with the Citadel alone the strip says one machine, and the card to add a second one marks the installer as coming soon", async () => {
  fakeServer({
    ...base,
    "GET /api/guild": () => ({ body: view({ single: true, machines: [machine({})] }) }),
  });
  renderApp("/guilda");
  expect(await screen.findByText("1 máquina, e é a Cidadela")).toBeTruthy();
  expect(screen.getByText("Época 4")).toBeTruthy();
  expect(screen.getByRole("link", { name: /Dev-1/ }).getAttribute("href")).toBe(
    "/workspaces/loja/mesas/login",
  );
  const card = screen.getByRole("region", { name: "Adicionar uma segunda máquina" });
  const step = within(card)
    .getByText(/Instalar o Mestre na máquina nova/)
    .closest("li");
  expect(within(step as HTMLElement).getByText("em breve")).toBeTruthy();
  // only the installer is coming soon; the other three steps are doable now
  expect(within(card).getAllByText("em breve")).toHaveLength(1);
  expect(within(card).getAllByRole("listitem")).toHaveLength(4);
  expect(document.body.textContent).not.toMatch(/\bM7\b/);
});

// CA-ADM-08
test("the machine opens as one line and keeps the sheet of the machine folded", async () => {
  fakeServer({
    ...base,
    "GET /api/guild": () => ({ body: view({ single: true, machines: [machine({})] }) }),
  });
  renderApp("/guilda");
  const row = await screen.findByRole("listitem", { name: "casa" });
  expect(row.textContent).toContain("você está aqui");
  const details = row.querySelector("details") as HTMLDetailsElement;
  expect(details.open).toBe(false);
  expect(within(details).getByText("Detalhes da máquina")).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Guilda", level: 1 })).toBeTruthy();
});

test("with a Tower in the Guild the band is gone and the row shows the replica and the load", async () => {
  fakeServer({ ...base, ...owner });
  renderApp("/guilda");
  const row = await screen.findByRole("listitem", { name: "torre" });
  expect(screen.queryByText("1 máquina, e é a Cidadela")).toBeNull();
  expect(screen.getByText(/2 máquinas; a Cidadela é casa/)).toBeTruthy();
  expect(within(row).getByText("Torre")).toBeTruthy();
  // how the machine stands is a chip beside its role, not words in the line below
  expect(within(row).getByText("Conectada")).toBeTruthy();
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
    (screen.getByRole("button", { name: "Passar a Cidadela para torre" }) as HTMLButtonElement)
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
  await user.click(await screen.findByRole("button", { name: "Passar a Cidadela para torre" }));
  await user.click(screen.getByRole("button", { name: "Passar a Cidadela para torre" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Passar a Cidadela para torre" }));
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

const joinView = (o: Record<string, unknown> = {}) =>
  view({
    pending: [{ id: "j1", name: "notebook", code: "482 917", created_at: "2026-10-26T12:00:00Z" }],
    ...o,
  });
const typeCode = async (user: ReturnType<typeof userEvent.setup>, code: string) =>
  fill(user, await screen.findByLabelText(/Código que a máquina nova/), code);

// CA-CRESC-09: the request is the news of the screen, in the strip and in its own card
test("a request to join is announced by the strip and the card says who, when and what to do", async () => {
  fakeServer({ ...base, "GET /api/guild": () => ({ body: joinView() }) });
  renderApp("/guilda");
  const card = (
    await screen.findByRole("heading", { name: "notebook quer entrar na Guilda" })
  ).closest("li") as HTMLElement;
  expect(screen.getAllByText("notebook quer entrar na Guilda").length).toBe(2);
  expect(screen.getByText("Compare o código de entrada e decida.")).toBeTruthy();
  expect(within(card).getByText(/Olhe a tela da máquina nova/)).toBeTruthy();
  expect(within(card).getByPlaceholderText("000 000")).toBeTruthy();
  expect(within(card).getByText(/Chegou mais de um pedido ao mesmo tempo/)).toBeTruthy();
  expect(within(card).getByText(/No Telegram a pergunta é a mesma/)).toBeTruthy();
});

// CA-CRESC-09
test("a request without a code (the machine did not reveal it yet) has no accept button and says it waits", async () => {
  fakeServer({
    ...base,
    "GET /api/guild": () => ({
      body: view({
        pending: [{ id: "j1", name: "notebook", created_at: "2026-10-26T12:00:00Z" }],
      }),
    }),
  });
  renderApp("/guilda");
  expect(await screen.findByText(/Esperando a máquina mostrar o código/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Aceitar" })).toBeNull();
  expect(screen.getByRole("button", { name: "Recusar" })).toBeTruthy();
});

// CA-CRESC-10
test("the code of the request is never shown: a wrong typed code keeps Aceitar off, the right one turns it on, and both ways ask the PIN", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/guild": () => ({ body: joinView() }),
    "POST /api/guild/joins/j1/decide": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  await typeCode(user, "482 918");
  expect(document.body.textContent).not.toContain("482 917");
  expect(document.body.textContent).not.toContain("482917");
  expect((screen.getByRole("button", { name: "Aceitar" }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  expect((await screen.findByText(/Os códigos não batem/)).textContent).toContain("recuse");
  await user.clear(screen.getByLabelText(/Código que a máquina nova/));
  await fill(user, screen.getByLabelText(/Código que a máquina nova/), "482 917");
  expect((screen.getByRole("button", { name: "Aceitar" }) as HTMLButtonElement).disabled).toBe(
    false,
  );
  await user.click(screen.getByRole("button", { name: "Aceitar" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Aceitar" }));
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/decide"))).toBe(true));
  const post = calls.find((c) => c.path.endsWith("/decide"));
  expect(post?.body).toEqual({ accept: true, excluded: false });
  expect(post?.headers["X-Mestre-Pin"]).toBe("123456");
});

// CA-CRESC-10 (refuse)
test("refusing also asks the PIN, whether or not the codes match", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/guild": () => ({ body: joinView() }),
    "POST /api/guild/joins/j1/decide": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  await user.click(await screen.findByRole("button", { name: "Recusar" }));
  expect(calls.some((c) => c.path.endsWith("/decide"))).toBe(false);
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Recusar" }));
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/decide"))).toBe(true));
  expect(calls.find((c) => c.path.endsWith("/decide"))?.body).toEqual({
    accept: false,
    excluded: false,
  });
});

// CA-CRESC-11
test("the box 'Máquina que não deve guardar segredos' sends excluded, renames the button and warns about the database", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/guild": () => ({ body: joinView() }),
    "POST /api/guild/joins/j1/decide": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  await typeCode(user, "482917");
  await user.click(screen.getByRole("checkbox", { name: /Máquina que não deve guardar segredos/ }));
  expect(screen.getByText(/continua recebendo o banco e os arquivos/)).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Aceitar como excluída" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Aceitar como excluída" }));
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/decide"))).toBe(true));
  expect(calls.find((c) => c.path.endsWith("/decide"))?.body).toEqual({
    accept: true,
    excluded: true,
  });
});

// CA-CRESC-19
test("two requests at once bring the notice to refuse them all", async () => {
  fakeServer({
    ...base,
    "GET /api/guild": () => ({
      body: view({
        pending: [
          { id: "j1", name: "notebook", code: "111 111", created_at: "2026-10-26T12:00:00Z" },
          { id: "j2", name: "desktop", code: "222 222", created_at: "2026-10-26T12:01:00Z" },
        ],
      }),
    }),
  });
  renderApp("/guilda");
  expect((await screen.findByRole("alert")).textContent).toContain("recuse todos");
});

// CA-CRESC-12
test("a Tower that just came in shows the AI accounts as 'não sei' with a link, never as done", async () => {
  fakeServer({ ...base, ...owner });
  renderApp("/guilda");
  const check = await screen.findByRole("region", { name: "Conferir torre" });
  expect(within(check).getByText(/Contas de IA: não sei/)).toBeTruthy();
  expect(
    within(check).getByRole("link", { name: "Conferir em Perfis de conta" }).getAttribute("href"),
  ).toBe("/perfis");
  expect(within(check).queryByText(/Contas de IA: (feito|prontas)/)).toBeNull();
});

// CA-CRESC-13
test("Passar a Cidadela is off with the reason for an excluded, an outside and a silent Tower, and on a fit one it shows the summary before the PIN", async () => {
  const bad = (id: string, o: Record<string, unknown>) =>
    ({ ...tower, id, name: id, agents: [], ...o }) as Record<string, unknown>;
  fakeServer({
    ...base,
    "GET /api/guild": () => ({
      body: view({
        machines: [
          machine({}),
          bad("excl", { excluded: true }),
          bad("fora", { outside_network: true }),
          bad("muda", { online: false }),
          bad("apta", {}),
        ],
      }),
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  for (const [name, why] of [
    ["excl", /Máquina excluída não vira Cidadela/],
    ["fora", /Fora da rede local/],
    ["muda", /Só uma máquina online recebe a Cidadela/],
  ] as const) {
    const row = await screen.findByRole("listitem", { name });
    expect(
      (
        within(row).getByRole("button", {
          name: `Passar a Cidadela para ${name}`,
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(within(row).getByText(why)).toBeTruthy();
  }
  const fit = await screen.findByRole("listitem", { name: "apta" });
  await user.click(within(fit).getByRole("button", { name: "Passar a Cidadela para apta" }));
  const summary = within(fit).getByRole("group", { name: "Passar a Cidadela para apta" });
  expect(within(summary).getByText(/casa vira Torre/)).toBeTruthy();
  expect(within(summary).getByText(/Os 1 agentes de casa ficam interrompidos/)).toBeTruthy();
  expect(within(summary).getByText(/60 segundos/)).toBeTruthy();
  expect(within(fit).queryByLabelText("PIN")).toBeNull(); // the summary comes before the PIN
});

// CA-CRESC-14
test("with the Citadel suspended the timeline shows only what the snapshot says and promises no time", async () => {
  fakeServer({
    ...base,
    "GET /api/guild": () => ({
      body: view({ machines: [machine({ suspended: true }), tower] }),
    }),
  });
  renderApp("/guilda");
  const line = await screen.findByRole("region", { name: "Troca da Cidadela em andamento" });
  expect(within(line).getByText("casa parou de agir.")).toBeTruthy();
  expect(line.textContent).not.toMatch(/\d+\s*(s|seg|min)/i);
  expect(line.textContent).not.toMatch(/prazo/i);
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
  expect((await screen.findByRole("alert")).textContent).toContain(
    "Esta ação exige o nível Senhor da Guilda; o seu é Administrador.",
  );
  expect(screen.queryByRole("link", { name: "Guilda" })).toBeNull();
  // a level below the Guildmaster gets no timeline: the shell's band is all it sees
  expect(screen.queryByRole("region", { name: "Troca da Cidadela em andamento" })).toBeNull();
});

test("if the typed code stops matching while the PIN is open, the PIN field closes and nothing is sent", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/guild": () => ({ body: joinView() }),
    "POST /api/guild/joins/j1/decide": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/guilda");
  await typeCode(user, "482917");
  await user.click(screen.getByRole("button", { name: "Aceitar" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await fill(user, screen.getByLabelText(/Código que a máquina nova/), "9");
  await waitFor(() => expect(screen.queryByLabelText("PIN")).toBeNull());
  expect(calls.some((c) => c.path.endsWith("/decide"))).toBe(false);
});
