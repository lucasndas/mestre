import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { fakeServer, fill, ME, renderApp } from "../../test-utils";

const statusText = () =>
  screen
    .getAllByRole("status")
    .map((e) => e.textContent)
    .join(" | ");
const adminMe = () => ({
  body: {
    ...ME,
    user: { ...ME.user, level: "administrator", level_name: "Administrador" },
    capabilities: ["approve_workspace_cls", "status"],
  },
});
const fail =
  (status: number, code: string, params: Record<string, unknown> = {}) =>
  () => ({ status, body: { error: { code, message: "boom: db locked", params } } });

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const OWNER_CAPS = [
  "approve_workspace_cls",
  "bases_vault_policy",
  "github_apps",
  "account_profiles",
  "status",
];
const base = {
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/me": () => ({ body: { ...ME, capabilities: OWNER_CAPS } }),
  "GET /api/transfer": () => ({ body: {} }),
};
const userEv = () => userEvent.setup({ delay: null, pointerEventsCheck: 0 });

const count = (total: number, others = 0) => ({
  mine: total - others,
  others,
  total,
  limit: 100,
  warn: 90,
  near: total >= 90,
});

const classRow = {
  name: "revisor",
  scope: "workspace",
  workspace: "loja",
  table: "",
  state: "approved",
  version: 2,
  app: "mestre-revisor",
  app_state: "pronto",
  problems: [],
};
const proposal = (o: Record<string, unknown> = {}) => ({
  id: 7,
  workspace: "loja",
  name: "testador",
  scope: "workspace",
  table: "",
  origin: "request",
  by: "Grão-Mestre",
  hash: "abc123",
  problems: [],
  tested: false,
  created_at: "2026-10-26T12:00:00Z",
  ...o,
});

// Part of CA-ADM-02 (the band), CA-CLASSES-06 and CA-CLI-CLR-03 (the panel side)
test("the classes screen lists the classes, warns at 90 Apps and approves a proposal with the PIN", async () => {
  let approved = false;
  const { calls } = fakeServer({
    ...base,
    "GET /api/classes": () => ({
      body: {
        apps: count(90),
        classes: [classRow],
        proposals: approved
          ? []
          : [
              proposal(),
              proposal({ id: 8, name: "quebrada", origin: "disk", problems: ["falta o prompt"] }),
            ],
      },
    }),
    "POST /api/classes/proposals/7/approve": () => {
      approved = true;
      return { body: { ok: true } };
    },
  });
  const user = userEv();
  renderApp("/classes");
  expect(await screen.findByText(/90 de 100 Apps na conta/)).toBeTruthy();
  expect(screen.getByText(/mestre-revisor · pronto/)).toBeTruthy();
  expect(screen.getAllByText("não testada").length).toBe(2);
  expect(screen.getByText("falta o prompt")).toBeTruthy();
  // a proposal with problems has no Approve button at all
  expect(screen.queryByRole("button", { name: "Aprovar quebrada" })).toBeNull();
  await user.click(screen.getByRole("button", { name: "Aprovar testador" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Aprovar testador" }));
  expect(await screen.findByText("Classe testador aprovada.")).toBeTruthy();
  const post = calls.find((c) => c.method === "POST");
  expect(post?.headers["X-Mestre-Pin"]).toBe("123456");
  expect(post?.body).toEqual({ workspace: "loja", hash: "abc123" });
});

test("a proposal that changed meanwhile says so and nothing is approved", async () => {
  fakeServer({
    ...base,
    "GET /api/classes": () => ({ body: { apps: count(3), classes: [], proposals: [proposal()] } }),
    "POST /api/classes/proposals/7/approve": () => ({
      status: 409,
      body: { error: { code: "proposal_stale", message: "x" } },
    }),
  });
  const user = userEv();
  renderApp("/classes");
  await user.click(await screen.findByRole("button", { name: "Aprovar testador" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Aprovar testador" }));
  expect((await screen.findByRole("alert")).textContent).toContain("A classe mudou");
  expect(screen.queryByText(/Apps na conta/)).toBeNull(); // no band at 3 Apps
});

// Part of CA-ADM-02
test("the Owner edits the other Apps and sees the creation link once", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/github/apps": () => ({
      body: {
        count: count(4, 2),
        creation: true,
        requests: [
          {
            app: "mestre-revisor",
            status: "expired",
            expires_at: "2026-10-26T11:00:00Z",
            expired: true,
          },
        ],
        apps: [
          {
            name: "mestre-revisor",
            slug: "",
            role: "classe",
            class: "revisor",
            state: "expired",
            state_label: "com erro",
            repos: [],
            request: "expired",
            can_link: true,
          },
        ],
      },
    }),
    "PUT /api/github/apps/others": () => ({ body: count(5, 3) }),
    "POST /api/github/apps/mestre-revisor/link": () => ({
      body: {
        app: "mestre-revisor",
        url: "https://x.test/github/apps/novo/tok",
        expires_at: "2026-10-26T13:00:00Z",
      },
    }),
  });
  const user = userEv();
  renderApp("/apps");
  expect(await screen.findByText("4 de 100 Apps na conta")).toBeTruthy();
  await user.click(screen.getByText(/Informar quantos são/));
  const field = screen.getByLabelText("Outros Apps da conta");
  await user.clear(field);
  await fill(user, field, "3");
  await user.click(screen.getByRole("button", { name: "Salvar" }));
  expect(await screen.findByText("Número de outros Apps atualizado.")).toBeTruthy();
  expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ count: 3 });
  await user.click(screen.getByRole("button", { name: "Gerar novo link" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Gerar novo link" }));
  expect((await screen.findByTestId("shown-once")).textContent).toBe(
    "https://x.test/github/apps/novo/tok",
  );
  await user.click(screen.getByRole("button", { name: "Já copiei" }));
  expect(screen.queryByTestId("shown-once")).toBeNull();
});

test("an Administrator sees the Apps and none of the Owner's actions", async () => {
  fakeServer({
    ...base,
    "GET /api/me": () => ({
      body: {
        ...ME,
        user: { ...ME.user, level: "administrator" },
        capabilities: ["approve_workspace_cls", "status"],
      },
    }),
    "GET /api/github/apps": () => ({
      body: { count: count(1), creation: true, requests: [], apps: [] },
    }),
  });
  renderApp("/apps");
  expect(await screen.findByText("1 de 100 Apps na conta")).toBeTruthy();
  expect(screen.queryByLabelText("Outros Apps da conta")).toBeNull();
});

// Part of CA-ADM-05: a sheet exported and imported back
test("a sheet is exported as YAML and imported back, replacing the one with the same name", async () => {
  const downloads: string[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    downloads.push(this.download);
  });
  URL.createObjectURL = vi.fn(() => "blob:x");
  URL.revokeObjectURL = vi.fn();
  const { calls } = fakeServer({
    ...base,
    "GET /api/presets": () => ({
      body: {
        bases: [
          {
            name: "claude-code",
            command: "claude",
            ready: "delay",
            platforms: ["linux"],
            builtin: false,
            yaml: "name: claude-code\n",
          },
          {
            name: "operador",
            command: "x",
            ready: "delay",
            platforms: ["windows"],
            builtin: true,
            yaml: "name: operador\n",
          },
        ],
        sheets: [
          {
            name: "Dev Claude",
            base: "claude-code",
            agent: "claude",
            class: "",
            account: "",
            lifecycle: "",
            incomplete: "",
            yaml: "name: Dev Claude\n",
          },
        ],
      },
    }),
    "GET /api/presets/export?format=yaml&sheets=Dev+Claude": () => ({
      body: "kind: mestre-presets\nsheets:\n  - name: Dev Claude\n",
    }),
    "POST /api/presets/import/plan": () => ({
      body: {
        bases: 0,
        sheets: 1,
        conflicts: [{ kind: "sheet", name: "Dev Claude" }],
        warnings: [],
      },
    }),
    "POST /api/presets/import": () => ({ body: { added: 0, replaced: 1, renamed: 0, skipped: 0 } }),
  });
  const user = userEv();
  renderApp("/bases-e-fichas");
  await screen.findByRole("heading", { name: "Fichas" });
  // the built-in base cannot be deleted
  expect(screen.queryByRole("button", { name: "Apagar operador" })).toBeNull();
  expect(screen.getByRole("button", { name: "Apagar claude-code" })).toBeTruthy();
  await user.click(screen.getByRole("checkbox", { name: "Dev Claude" }));
  await user.click(screen.getByRole("button", { name: "Exportar" }));
  expect(await screen.findByText("Arquivo exportado.")).toBeTruthy();
  expect(downloads).toEqual(["mestre-presets.yaml"]);

  await fill(user, screen.getByLabelText("Ou cole o conteúdo"), "kind: mestre-presets\n");
  await user.click(screen.getByRole("button", { name: "Conferir o arquivo" }));
  await user.selectOptions(await screen.findByLabelText("Ficha Dev Claude"), "replace");
  await user.click(screen.getByRole("button", { name: "Importar" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Importar" }));
  expect((await screen.findByText(/Importação concluída/)).textContent).toContain(
    "substituídas: 1",
  );
  const post = calls.find((c) => c.path === "/api/presets/import");
  expect(post?.headers["X-Mestre-Pin"]).toBe("123456");
  expect(post?.body).toEqual({
    content: "kind: mestre-presets\n",
    choices: [{ kind: "sheet", name: "Dev Claude", action: "replace", new_name: "" }],
  });
});

test("a YAML the server refuses shows its message and the editor stays open", async () => {
  fakeServer({
    ...base,
    "GET /api/presets": () => ({ body: { bases: [], sheets: [] } }),
    "PUT /api/presets/sheets/novo": () => ({
      status: 422,
      body: { error: { code: "preset_invalid", message: "linha 2: falta a base" } },
    }),
  });
  const user = userEv();
  renderApp("/bases-e-fichas");
  await user.click(await screen.findByRole("button", { name: "Criar uma ficha" }));
  await user.click(screen.getByRole("button", { name: "Salvar" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Salvar" }));
  expect((await screen.findByRole("alert")).textContent).toContain("linha 2: falta a base");
  expect(screen.getByRole("textbox", { name: "YAML" })).toBeTruthy();
});

// Part of CA-CLI-CLR-03
test("a profile shows its setup steps and the button goes on to the terminal that waits", async () => {
  fakeServer({
    ...base,
    "GET /api/profiles": () => ({
      body: {
        machine: "maq1",
        providers: ["claude-code", "codex"],
        setup: true,
        profiles: [
          {
            name: "trabalho",
            provider: "claude-code",
            machine: "maq1",
            dir: "/x",
            default: true,
            step: "login",
            detail: "o login no terminal",
            created_at: "2026-10-26T12:00:00Z",
          },
        ],
      },
    }),
    "POST /api/profiles/trabalho/advance": () => ({
      body: {
        step: "login",
        done: false,
        waiting: "faça o login",
        terminal: "term-9",
        conformant: false,
        reason: "",
      },
    }),
  });
  const user = userEv();
  renderApp("/perfis");
  const steps = await screen.findByRole("list", { name: "Etapas da configuração" });
  expect(within(steps).getByText("Instalar o programa do provedor").nextSibling?.textContent).toBe(
    "(feita)",
  );
  expect(steps.querySelector('[aria-current="step"]')?.textContent).toContain(
    "Entrar na conta do provedor",
  );
  await user.click(screen.getByRole("button", { name: "Continuar a configuração de trabalho" }));
  const link = await screen.findByRole("link", { name: "Abrir o terminal para continuar" });
  expect(link.getAttribute("href")).toBe("/terminal/term-9");
  expect(screen.getByText("Esperando: faça o login")).toBeTruthy();
});

test("without guided setup the screen says so and offers no advance button", async () => {
  fakeServer({
    ...base,
    "GET /api/profiles": () => ({
      body: { machine: "maq1", providers: ["codex"], setup: false, profiles: [] },
    }),
  });
  renderApp("/perfis");
  expect(await screen.findByText(/configuração guiada ainda não está disponível/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Continuar a configuração/ })).toBeNull();
});

// Part of CA-USO-08 (the panel side)
test("the usage shows the tables with the gaps as no data, and the limit that is left", async () => {
  fakeServer({
    ...base,
    "GET /api/usage?days=14": () => ({
      body: {
        today: "2026-10-26",
        days: [
          {
            key: "2026-10-25",
            label: "2026-10-25",
            workspace: "",
            cost_usd: 1.5,
            tokens: 12000,
            active_seconds: 5400,
            turns: 9,
          },
          { key: "2026-10-26", label: "2026-10-26", workspace: "", active_seconds: 60, turns: 1 },
        ],
        workspaces: [
          {
            key: "loja",
            label: "loja",
            workspace: "loja",
            cost_usd: 1.5,
            active_seconds: 5460,
            turns: 10,
          },
        ],
        tables: [],
        quests: [],
        characters: [
          { key: "c1", label: "Dev-1", workspace: "loja", active_seconds: 300, turns: 2 },
        ],
        accounts: [],
        limits: [
          {
            scope: "workspace",
            key: "loja",
            unit: "usd",
            value: 10,
            effective: 10,
            used: 2.5,
            left: 7.5,
            percent: 25,
            period: "day",
            has_data: true,
          },
        ],
      },
    }),
  });
  renderApp("/consumo");
  const daily = await screen.findByRole("table", { name: "Consumo por dia" });
  const rows = within(daily).getAllByRole("row");
  expect(within(rows[2] as HTMLElement).getAllByText("sem dado").length).toBe(2); // no cost, no tokens
  expect(within(rows[1] as HTMLElement).getByText("1,5 h")).toBeTruthy();
  const chars = screen.getByRole("table", { name: "Consumo por personagem" });
  expect(within(chars).getAllByText("sem dado").length).toBe(2);
  expect(screen.queryByRole("heading", { name: "Por conta" })).toBeNull();
  const bar = screen.getByRole("progressbar", { name: "Campanha loja" });
  expect(bar.getAttribute("value")).toBe("25");
  await waitFor(() => expect(screen.getByText(/Falta: US\$\s?7,50/)).toBeTruthy());
});

// CA-ADM-17
test("Perfis de conta with no data says what it is, shows the state and gives the way out", async () => {
  fakeServer({
    ...base,
    "GET /api/profiles": () => ({
      body: {
        machine: "maq1",
        providers: ["claude-code", "codex", "antigravity"],
        setup: false,
        profiles: [],
      },
    }),
  });
  renderApp("/perfis");
  await screen.findByText("Nenhuma conta de IA entrou ainda");
  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  expect(screen.getByText(/Sem uma conta entrada, nenhum agente roda\./)).toBeTruthy();
  expect(screen.getByText("Vocabulário:")).toBeTruthy();
  const strip = { textContent: statusText() };
  expect(strip.textContent).toContain("Nenhuma conta de IA entrou ainda");
  expect(strip.textContent).toContain("configuração guiada ainda não está disponível");
  // one card per provider: the empty state and the strip speak of the same data
  for (const name of ["Entrar com Claude", "Entrar com Codex", "Entrar com Antigravity"])
    expect(screen.getByRole("button", { name })).toBeTruthy();
});

// CA-ADM-17
test("Apps do GitHub with no App explains what an App is and the three steps", async () => {
  fakeServer({
    ...base,
    "GET /api/github/apps": () => ({
      body: { count: count(0), creation: true, requests: [], apps: [] },
    }),
  });
  renderApp("/apps");
  expect(await screen.findByRole("heading", { name: "O que é um App do GitHub" })).toBeTruthy();
  expect(statusText()).toContain("Nenhum App do GitHub ainda");
  expect(screen.getByText(/Abra o link no navegador em que você está logado/)).toBeTruthy();
  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
});

// CA-ADM-17
test("Consumo de uso and Bases e fichas with no data explain the empty and say what is missing", async () => {
  fakeServer({
    ...base,
    "GET /api/usage?days=14": () => ({
      body: {
        today: "2026-10-26",
        days: [],
        workspaces: [],
        tables: [],
        quests: [],
        characters: [],
        accounts: [],
        limits: [],
      },
    }),
    "GET /api/presets": () => ({ body: { bases: [], sheets: [] } }),
  });
  const first = renderApp("/consumo");
  expect((await screen.findAllByText("Nada gasto ainda")).length).toBeGreaterThan(1);
  expect(statusText()).toContain("Nada gasto ainda");
  first.unmount();
  renderApp("/bases-e-fichas");
  expect(await screen.findByRole("heading", { name: "Nenhuma ficha ainda" })).toBeTruthy();
  expect(statusText()).toContain("0 bases e 0 fichas");
  expect(screen.getByRole("button", { name: "Criar uma ficha" })).toBeTruthy();
});

// CA-ADM-18
test("a read that fails shows what, why, Tentar de novo and the raw answer folded away, not old data", async () => {
  let ok = false;
  fakeServer({
    ...base,
    "GET /api/classes": () =>
      ok
        ? { body: { apps: count(3), classes: [classRow], proposals: [] } }
        : fail(500, "internal")(),
  });
  const user = userEv();
  renderApp("/classes");
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("Não foi possível carregar as classes.");
  const details = within(alert).getByText("Detalhes técnicos").closest("details");
  expect(details?.open).toBe(false);
  expect(details?.textContent).toContain("internal: boom: db locked");
  expect(statusText()).not.toContain("em vigor"); // no strip: the state is not known
  ok = true;
  await user.click(within(alert).getByRole("button", { name: "Tentar de novo" }));
  expect(await screen.findByText(/mestre-revisor · pronto/)).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
});

// CA-ADM-18
test("each screen of the unit shows the same error block when its read fails", async () => {
  fakeServer({
    ...base,
    "GET /api/profiles": fail(500, "internal"),
    "GET /api/github/apps": fail(500, "internal"),
    "GET /api/presets": fail(500, "internal"),
    "GET /api/usage?days=14": fail(500, "internal"),
  });
  for (const [path, what] of [
    ["/perfis", "os perfis de conta"],
    ["/apps", "os Apps do GitHub"],
    ["/bases-e-fichas", "as bases e fichas"],
    ["/consumo", "o consumo"],
  ] as const) {
    const v = renderApp(path);
    expect((await screen.findByRole("alert")).textContent).toContain(
      `Não foi possível carregar ${what}.`,
    );
    expect(screen.getByRole("button", { name: "Tentar de novo" })).toBeTruthy();
    v.unmount();
  }
});

// CA-ADM-19
test("with no profile there is a card per provider and the profile is created with the PIN", async () => {
  let created = false;
  const { calls } = fakeServer({
    ...base,
    "GET /api/profiles": () => ({
      body: {
        machine: "maq1",
        providers: ["claude-code", "codex", "antigravity"],
        setup: false,
        profiles: created
          ? [
              {
                name: "claude-1",
                provider: "claude-code",
                machine: "maq1",
                dir: "/x",
                default: true,
                step: "install",
                detail: "",
                created_at: "2026-10-26T12:00:00Z",
              },
            ]
          : [],
      },
    }),
    "POST /api/profiles": () => {
      created = true;
      return { body: {} };
    },
  });
  const user = userEv();
  renderApp("/perfis");
  await user.click(await screen.findByRole("button", { name: "Entrar com Claude" }));
  expect((screen.getByLabelText("Nome do perfil") as HTMLInputElement).value).toBe("claude-1");
  await user.click(screen.getByRole("button", { name: "Criar perfil" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Criar perfil" }));
  expect(await screen.findByText("Perfil claude-1 criado.")).toBeTruthy();
  const post = calls.find((c) => c.method === "POST");
  expect(post?.headers["X-Mestre-Pin"]).toBe("123456");
  expect(post?.body).toEqual({ name: "claude-1", provider: "claude-code" });
  // without the guided setup the card says the login is by hand, and not checked
  expect(screen.getByText("Login feito à mão")).toBeTruthy();
  expect(screen.getByText("login não conferido")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Remover claude-1" })).toBeTruthy();
});

// CA-ADM-19
test("with the guided setup the same card shows the steps, the badge and the way on", async () => {
  fakeServer({
    ...base,
    "GET /api/profiles": () => ({
      body: {
        machine: "maq1",
        providers: ["codex"],
        setup: true,
        profiles: [
          {
            name: "codex-1",
            provider: "codex",
            machine: "maq1",
            dir: "/x",
            default: true,
            step: "hooks",
            detail: "aprovar os hooks",
            created_at: "2026-10-26T12:00:00Z",
          },
        ],
      },
    }),
  });
  renderApp("/perfis");
  expect(await screen.findByRole("list", { name: "Etapas da configuração" })).toBeTruthy();
  expect(screen.getByText("falta um passo")).toBeTruthy();
  expect(screen.getByText("Esperando: aprovar os hooks")).toBeTruthy();
  expect(statusText()).toContain("0 de 1 contas prontas");
  expect(screen.getByRole("button", { name: "Continuar a configuração de codex-1" })).toBeTruthy();
  expect(screen.queryByText("Login feito à mão")).toBeNull();
});

// CA-ADM-20
test("an App in creation and one with an expired link each say the next step, inside the App", async () => {
  fakeServer({
    ...base,
    "GET /api/github/apps": () => ({
      body: {
        count: count(2),
        creation: true,
        requests: [
          {
            app: "grao-mestre",
            status: "link",
            expires_at: "2026-10-26T13:00:00Z",
            expired: false,
          },
          {
            app: "mestre-revisor",
            status: "expired",
            expires_at: "2026-10-26T11:00:00Z",
            expired: true,
          },
        ],
        apps: [
          {
            name: "grao-mestre",
            slug: "",
            role: "fixo",
            class: "",
            state: "awaiting",
            state_label: "em criação",
            repos: [],
            request: "link",
            can_link: false,
          },
          {
            name: "mestre-revisor",
            slug: "",
            role: "classe",
            class: "revisor",
            state: "expired",
            state_label: "em criação",
            repos: [],
            request: "expired",
            can_link: true,
          },
        ],
      },
    }),
  });
  renderApp("/apps");
  await screen.findByText("O link venceu.");
  const cards = screen.getAllByRole("listitem");
  const gm = cards.find((c) => c.textContent?.includes("grao-mestre")) as HTMLElement;
  const rev = cards.find((c) => c.textContent?.includes("mestre-revisor")) as HTMLElement;
  expect(gm.textContent).toContain("Abra o link no navegador em que você está logado no GitHub");
  expect(within(gm).queryByRole("button")).toBeNull();
  expect(rev.textContent).toContain("O link venceu.");
  expect(within(rev).getByRole("button", { name: "Gerar novo link" })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Pedidos de criação" })).toBeNull();
  expect(statusText()).toContain("2 Apps esperando um passo seu");
});

// CA-ADM-22
test("a class whose App is in creation says it cannot open PRs, and a proposal with problems has no Approve", async () => {
  fakeServer({
    ...base,
    "GET /api/me": adminMe,
    "GET /api/classes": () => ({
      body: {
        apps: count(3),
        classes: [{ ...classRow, app_state: "em criação" }],
        proposals: [
          proposal({ id: 8, name: "quebrada", origin: "disk", problems: ["falta o prompt"] }),
        ],
      },
    }),
  });
  renderApp("/classes");
  expect(await screen.findByText(/Sem ele, a classe não consegue abrir PRs/)).toBeTruthy();
  const link = screen.getByRole("link", { name: "Gerar o link em Apps do GitHub" });
  expect(link.getAttribute("href")).toBe("/apps");
  expect(screen.getByText("falta o prompt")).toBeTruthy();
  expect(screen.getByText(/veja o teto da campanha/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Aprovar/ })).toBeNull();
  expect(statusText()).toContain("1 proposta esperando a sua aprovação");
});

// CA-ADM-22
test("with no class the empty state explains what a class is and how it is born", async () => {
  fakeServer({
    ...base,
    "GET /api/classes": () => ({ body: { apps: count(0), classes: [], proposals: [] } }),
  });
  renderApp("/classes");
  expect(
    await screen.findByRole("heading", { name: "O que é uma classe e como ela nasce" }),
  ).toBeTruthy();
  expect(screen.getByText(/O Grão-Mestre escreve a proposta/)).toBeTruthy();
  expect(screen.getAllByRole("button", { name: "Falar com o Grão-Mestre" }).length).toBeGreaterThan(
    0,
  );
});

// CA-ADM-24
test("on the screens of the unit the tables scroll inside their own container and the targets are 44 px", async () => {
  fakeServer({
    ...base,
    "GET /api/profiles": () => ({
      body: {
        machine: "maq1",
        providers: ["claude-code"],
        setup: true,
        profiles: [
          {
            name: "trabalho",
            provider: "claude-code",
            machine: "maq1",
            dir: "/x",
            default: false,
            step: "login",
            detail: "",
            created_at: "2026-10-26T12:00:00Z",
          },
        ],
      },
    }),
    "GET /api/presets": () => ({
      body: {
        bases: [
          {
            name: "claude-code",
            command: "claude",
            ready: "delay",
            platforms: ["linux"],
            builtin: false,
            yaml: "x",
          },
        ],
        sheets: [],
      },
    }),
  });
  for (const path of ["/perfis", "/bases-e-fichas"]) {
    const v = renderApp(path);
    await screen.findByRole("status");
    for (const table of v.container.querySelectorAll("table"))
      expect(table.parentElement?.className).toContain("overflow-x-auto");
    const targets = [...v.container.querySelectorAll("main button, main summary, main a")].filter(
      (el) => !(el.getAttribute("aria-label") ?? "").startsWith("Ajuda sobre"),
    );
    expect(targets.length).toBeGreaterThan(0);
    for (const el of targets) expect(el.className, el.outerHTML).toContain("min-h-11");
    v.unmount();
  }
});

// CA-ADM-25
test("an Administrator who opens a screen of the Guildmaster sees the level that is needed and no action", async () => {
  fakeServer({
    ...base,
    "GET /api/me": adminMe,
    "GET /api/profiles": fail(403, "forbidden_level", { need: "owner", have: "administrator" }),
  });
  renderApp("/perfis");
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("exige o nível Senhor da Guilda; o seu é Administrador");
  expect(screen.queryByRole("button", { name: /Entrar|Criar|Remover/ })).toBeNull();
});

// CA-ADM-25
test("an Administrator on Apps sees the main button off with the level it needs", async () => {
  fakeServer({
    ...base,
    "GET /api/me": adminMe,
    "GET /api/github/apps": () => ({
      body: {
        count: count(1),
        creation: true,
        requests: [],
        apps: [
          {
            name: "grao-mestre",
            slug: "",
            role: "fixo",
            class: "",
            state: "awaiting",
            state_label: "em criação",
            repos: [],
            request: "",
            can_link: false,
          },
        ],
      },
    }),
  });
  renderApp("/apps");
  const off = await screen.findByRole("button", { name: "Gerar o link do App grao-mestre" });
  expect((off as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getByText("Exige o nível Senhor da Guilda; o seu é Administrador.")).toBeTruthy();
  expect(screen.getByText(/Você só lê esta tela/)).toBeTruthy();
});
