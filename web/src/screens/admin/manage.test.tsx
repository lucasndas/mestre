import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { fakeServer, fill, ME, renderApp } from "../../test-utils";

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
  expect(screen.getByRole("cell", { name: /mestre-revisor/ })).toBeTruthy();
  expect(screen.getAllByText("não testada").length).toBe(2);
  expect(screen.getByText("falta o prompt")).toBeTruthy();
  // a proposal with problems cannot be approved
  expect(
    (screen.getByRole("button", { name: "Aprovar quebrada" }) as HTMLButtonElement).disabled,
  ).toBe(true);
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
  const field = screen.getByLabelText("Outros Apps da conta");
  await user.clear(field);
  await fill(user, field, "3");
  await user.click(screen.getByRole("button", { name: "Salvar" }));
  expect(await screen.findByText("Número de outros Apps atualizado.")).toBeTruthy();
  expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ count: 3 });
  await user.click(screen.getByRole("button", { name: "Novo link para mestre-revisor" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Novo link para mestre-revisor" }));
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
  expect(screen.queryByRole("link", { name: "Perfis de conta" })).toBeNull();
  expect(screen.getByRole("link", { name: "Classes" })).toBeTruthy();
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
  await user.click(await screen.findByRole("button", { name: "Nova ficha" }));
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
  const bar = screen.getByRole("progressbar", { name: "Workspace loja" });
  expect(bar.getAttribute("value")).toBe("25");
  await waitFor(() => expect(screen.getByText(/Falta: US\$\s?7,50/)).toBeTruthy());
});
