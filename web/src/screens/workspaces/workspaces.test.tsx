import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { fakeServer, fill, ME, renderApp } from "../../test-utils";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const ADMIN = ["status", "create_workspace", "configure_workspace", "pause", "decide_approvals"];
const as = (caps: string[]) => ({ "GET /api/me": () => ({ body: { ...ME, capabilities: caps } }) });
const base = {
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/transfer": () => ({ body: {} }),
};

const stage = (key: string, state: string, extra: object = {}) => ({ key, state, ...extra });
const row = (o: Record<string, unknown>) => ({
  slug: "loja",
  repo: "acme/loja",
  default_branch: "main",
  state: "active",
  stages: ["apps", "mirror", "files", "analysis", "approval_test"].map((k) => stage(k, "done")),
  versioning: "local",
  versioning_asked: true,
  grand_master: "idle",
  grand_master_sheet: "gm-claude",
  created_at: "2026-10-20T12:00:00Z",
  can_act: true,
  ...o,
});
const list = (workspaces: unknown[], extra: object = {}) => ({
  workspaces,
  sheets: [{ name: "gm-claude", agent: "claude-code" }],
  can_create: true,
  ...extra,
});

test("the list shows the workspaces and the nav links to it", async () => {
  fakeServer({
    ...base,
    ...as(["status"]),
    "GET /api/workspaces": () => ({ body: list([row({})], { can_create: false, sheets: [] }) }),
  });
  renderApp("/workspaces");
  const article = await screen.findByRole("article", { name: "loja" });
  expect(within(article).getByText("acme/loja")).toBeTruthy();
  expect(within(article).getByText("Ativa")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Criar campanha" })).toBeNull();
  expect(screen.getByRole("link", { name: "Campanhas" })).toBeTruthy();
});

test("an Administrator creates a workspace with the sheet of the Grand Master", async () => {
  let made = false;
  const { calls } = fakeServer({
    ...base,
    ...as(ADMIN),
    "GET /api/workspaces": () => ({
      body: list(made ? [row({ slug: "novo", repo: "acme/novo", state: "preparing" })] : []),
    }),
    "POST /api/workspaces": () => {
      made = true;
      return { status: 201, body: row({ slug: "novo", repo: "acme/novo", state: "preparing" }) };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces");
  await user.click(await screen.findByRole("button", { name: "Criar campanha" }));
  const submit = screen.getByRole("button", { name: "Criar campanha" });
  expect((submit as HTMLButtonElement).disabled).toBe(true); // no repository yet
  await fill(user, screen.getByLabelText("Repositório"), "acme/novo");
  await user.click(screen.getByRole("button", { name: "Criar campanha" }));
  expect(await screen.findByText("Campanha novo criada. A sessão zero começou.")).toBeTruthy();
  const post = calls.find((c) => c.method === "POST" && c.path === "/api/workspaces");
  expect(post?.body).toEqual({ repo: "acme/novo", grand_master_sheet: "gm-claude" });
  expect(await screen.findByRole("article", { name: "novo" })).toBeTruthy();
});

test("a creation the engine refuses shows the reason in the words of the screen", async () => {
  fakeServer({
    ...base,
    ...as(ADMIN),
    "GET /api/workspaces": () => ({ body: list([]) }),
    "POST /api/workspaces": () => ({
      status: 409,
      body: {
        error: { code: "refused", message: "x", params: { reason: "repo_taken", slug: "loja" } },
      },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces");
  await user.click(await screen.findByRole("button", { name: "Criar campanha" }));
  await fill(user, screen.getByLabelText("Repositório"), "acme/loja");
  await user.click(screen.getByRole("button", { name: "Criar campanha" }));
  expect((await screen.findByRole("alert")).textContent).toBe(
    "Esse repositório já é a campanha loja.",
  );
});

test("the steps of the session zero show the failed one, and retrying asks the server", async () => {
  const failing = row({
    state: "preparing",
    stages: [
      stage("apps", "done"),
      stage("mirror", "done"),
      stage("files", "done"),
      stage("analysis", "failed", {
        failure: {
          code: "failed",
          reason: "o agente não rodou",
          failures: 2,
          at: "2026-10-20T12:00:00Z",
          retry_at: "2026-10-20T12:05:00Z",
        },
      }),
      stage("approval_test", "pending"),
    ],
  });
  const { calls } = fakeServer({
    ...base,
    ...as(ADMIN),
    "GET /api/workspaces": () => ({ body: list([failing]) }),
    "POST /api/workspaces/loja/retry": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces");
  expect(await screen.findByText("Análise do projeto")).toBeTruthy();
  expect(
    screen.getByText("Falhou: o agente não rodou (2 vezes; o Mestre tenta de novo sozinho)."),
  ).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Tentar de novo a etapa analysis de loja" }));
  await waitFor(() =>
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/workspaces/loja/retry")).toBe(
      true,
    ),
  );
});

test("a stage that waits says what for, and an Observer sees no button", async () => {
  const waiting = row({
    state: "preparing",
    stages: [
      stage("apps", "waiting", { waiting: "Apps ainda não instalados" }),
      stage("mirror", "pending"),
    ],
  });
  fakeServer({
    ...base,
    ...as(["status"]),
    "GET /api/workspaces": () => ({ body: list([waiting], { can_create: false, sheets: [] }) }),
  });
  renderApp("/workspaces");
  expect(await screen.findByText("Esperando: Apps ainda não instalados")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Tentar de novo/ })).toBeNull();
});

test("pausing and resuming ask the server and show the refusal", async () => {
  const { calls } = fakeServer({
    ...base,
    ...as(ADMIN),
    "GET /api/workspaces": () => ({ body: list([row({})]) }),
    "POST /api/workspaces/loja/pause": () => ({
      status: 409,
      body: { error: { code: "refused", message: "x", params: { reason: "bad_state" } } },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces");
  await user.click(await screen.findByRole("button", { name: "Pausar campanha" }));
  expect((await screen.findByRole("alert")).textContent).toBe(
    "O estado da campanha não permite isso agora.",
  );
  expect(calls.filter((c) => c.path === "/api/workspaces/loja/pause")).toHaveLength(1);
});

test("the question of versioning is answered by an Administrator and waits for the others", async () => {
  const asked = row({ versioning: "", versioning_asked: true });
  const { calls } = fakeServer({
    ...base,
    ...as(ADMIN),
    "GET /api/workspaces": () => ({ body: list([asked]) }),
    "POST /api/workspaces/loja/versioning": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces");
  await screen.findByRole("form", { name: "Pergunta pendente: versionar o .mestre/?" });
  await user.click(screen.getByRole("radio", { name: /Versionar no projeto/ }));
  await user.click(screen.getByRole("button", { name: "Responder" }));
  await waitFor(() =>
    expect(calls.find((c) => c.path === "/api/workspaces/loja/versioning")?.body).toEqual({
      answer: "versioned",
    }),
  );
});

test("an Approver is told an Administrator answers the question of versioning", async () => {
  fakeServer({
    ...base,
    ...as(["status", "decide_approvals"]),
    "GET /api/workspaces": () => ({
      body: list([row({ versioning: "" })], { can_create: false, sheets: [] }),
    }),
  });
  renderApp("/workspaces");
  expect(
    await screen.findByText("Pergunta pendente sobre o .mestre/. Um Administrador responde."),
  ).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Responder" })).toBeNull();
});

// CA-ADM-15: an Administrator edits the proposal and confirms it; an Approver does not see the action.
test("an Administrator edits and confirms the verification commands, and an outside edit is flagged", async () => {
  const proposal = 'version: 1\ncomandos: ["go build ./..."]\n';
  let confirmed: unknown = null;
  const view = (extra: object = {}) => ({
    file: proposal,
    commands: ["go build ./..."],
    outside: false,
    ...extra,
  });
  const { calls } = fakeServer({
    ...base,
    ...as(ADMIN),
    "GET /api/workspaces": () => ({ body: list([row({})]) }),
    "GET /api/workspaces/loja/verification": () => ({
      body: confirmed ? view({ confirmed, outside: true }) : view(),
    }),
    "PUT /api/workspaces/loja/verification": (c) => {
      confirmed = {
        doc: (c.body as { doc: string }).doc,
        hash: "h1",
        by: "Maria Souza",
        at: "2026-10-26T12:00:00Z",
      };
      return { body: view({ file: (c.body as { doc: string }).doc, confirmed }) };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces");
  await user.click(await screen.findByRole("button", { name: "Comandos de verificação" }));
  expect(await screen.findByText("Nenhuma versão confirmada")).toBeTruthy();
  const editor = screen.getByLabelText("verificacao.yaml");
  expect((editor as HTMLTextAreaElement).value).toBe(proposal);
  const edited = 'version: 1\ncomandos: ["go build ./...", "go test ./..."]\n';
  await user.clear(editor);
  await fill(user, editor, edited);
  await user.click(screen.getByRole("button", { name: "Confirmar estes comandos" }));
  expect(await screen.findByText("Versão confirmada. Ela já vale.")).toBeTruthy();
  expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ doc: edited });
  expect(screen.getByText(/Em vigor: confirmada por Maria Souza/)).toBeTruthy();
});

test("a verification file that the engine refuses is not confirmed and says why", async () => {
  fakeServer({
    ...base,
    ...as(ADMIN),
    "GET /api/workspaces": () => ({ body: list([row({})]) }),
    "GET /api/workspaces/loja/verification": () => ({
      body: {
        file: "version: 1",
        commands: [],
        outside: true,
        file_error: "no command",
        confirmed: {
          doc: 'version: 1\ncomandos: ["make"]\n',
          hash: "h",
          by: "Ana",
          at: "2026-10-26T12:00:00Z",
        },
      },
    }),
    "PUT /api/workspaces/loja/verification": () => ({
      status: 409,
      body: { error: { code: "refused", message: "x", params: { reason: "invalid_document" } } },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces");
  await user.click(await screen.findByRole("button", { name: "Comandos de verificação" }));
  expect(await screen.findByText(/O arquivo foi mudado fora do painel/)).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Confirmar estes comandos" }));
  expect(
    (await screen.findAllByRole("alert")).some((a) =>
      a.textContent?.includes("Nada foi confirmado"),
    ),
  ).toBe(true);
});

test("an Approver reads the verification commands but cannot confirm them", async () => {
  fakeServer({
    ...base,
    ...as(["status", "decide_approvals"]),
    "GET /api/workspaces": () => ({ body: list([row({})], { can_create: false, sheets: [] }) }),
    "GET /api/workspaces/loja/verification": () => ({
      body: { file: "version: 1", commands: ["make"], outside: false },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces");
  await user.click(await screen.findByRole("button", { name: "Comandos de verificação" }));
  expect(await screen.findByText("Um Administrador confirma os comandos.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Confirmar estes comandos" })).toBeNull();
});

test("the link of the session zero pull request is a link only for github.com", async () => {
  fakeServer({
    ...base,
    ...as(["status"]),
    "GET /api/workspaces": () => ({
      body: list(
        [
          row({ slug: "a", repo: "acme/a", session_zero_pr: "https://github.com/acme/a/pull/1" }),
          row({ slug: "b", repo: "acme/b", session_zero_pr: "javascript:alert(1)" }),
        ],
        { can_create: false, sheets: [] },
      ),
    }),
  });
  renderApp("/workspaces");
  expect(
    (await screen.findByRole("link", { name: "https://github.com/acme/a/pull/1" })).getAttribute(
      "href",
    ),
  ).toBe("https://github.com/acme/a/pull/1");
  expect(screen.queryByRole("link", { name: "javascript:alert(1)" })).toBeNull();
});

// docs/painel/workspaces.md: the card says how many tables go and how many wait for the user, from the view of the campaign
test("the card of a campaign says how many tables are going and how many wait for the user", async () => {
  const block = (id: string, extra: object = {}) => ({
    id,
    epic: 1,
    epic_title: id,
    branch: `epico/${id}`,
    state: "working",
    paused: false,
    party: [],
    party_size: 0,
    waits: [],
    stalled: false,
    quiet_seconds: 0,
    pr: 0,
    ...extra,
  });
  fakeServer({
    ...base,
    ...as(ADMIN),
    "GET /api/workspaces": () => ({ body: list([row({})]) }),
    "GET /api/workspaces/loja/view": () => ({
      body: {
        workspace: row({}),
        tables: [
          block("a"),
          block("b", { waits: [{ kind: "same_failure", reason: "mesma falha" }] }),
        ],
        layout: [],
        can_pause: true,
        can_type: true,
      },
    }),
  });
  renderApp("/workspaces");
  const article = await screen.findByRole("article", { name: "loja" });
  expect(await within(article).findByText("2 mesas em andamento · 1 espera por você")).toBeTruthy();
});
