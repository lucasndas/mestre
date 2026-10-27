import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type {
  ApprovalItem,
  ChatConversation,
  ProfileRow,
  TableBlock,
  UserRow,
  WorkspaceRow,
  WorkspaceView,
} from "../../gen/panel";
import { emit, installFakeSocket, openSocket } from "../../test-socket";
import { fakeServer, ME, renderApp } from "../../test-utils";

beforeEach(() => {
  installFakeSocket();
  localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

const OWNER_CAPS = [
  "status",
  "talk_to_grand_master",
  "decide_approvals",
  "manage_users",
  "manage_machines",
  "github_apps",
  "account_profiles",
];

const row = (o: Partial<WorkspaceRow> = {}): WorkspaceRow => ({
  slug: "loja",
  repo: "acme/loja",
  default_branch: "main",
  state: "active",
  stages: [],
  versioning: "local",
  versioning_asked: true,
  grand_master: "idle",
  created_at: "2026-10-20T12:00:00Z",
  can_act: true,
  ...o,
});

const table = (o: Partial<TableBlock> = {}): TableBlock => ({
  id: "42-login",
  epic: 42,
  epic_title: "Login com Google",
  branch: "epico/42-login",
  state: "working",
  paused: false,
  party: [{ machine: "m1", machine_name: "Cidadela", characters: 3 }],
  party_size: 3,
  waits: [],
  last_delivery: "2026-10-26T14:00:00Z",
  stalled: false,
  quiet_seconds: 0,
  pr: 0,
  ...o,
});

const view = (tables: TableBlock[]): WorkspaceView => ({
  workspace: row(),
  tables,
  layout: [],
  can_pause: true,
  can_type: true,
});

const request = (o: Partial<ApprovalItem> = {}): ApprovalItem => ({
  id: 5,
  workspace: "loja",
  table: "42-login",
  type: "custo_extra",
  risk: "medium",
  state: "open",
  about: "mais US$ 20 de uso este mês",
  text: "",
  agent: "",
  requester: "",
  created_at: "2026-10-26T14:00:00Z",
  approvals: 1,
  counted: 0,
  voted: false,
  answers: [{ id: "approve", label: "Aprovar" } as never],
  members: [],
  ...o,
});

const profile = (o: Partial<ProfileRow> = {}): ProfileRow => ({
  name: "claude-ana",
  provider: "claude",
  machine: "m1",
  dir: "/x",
  default: true,
  step: "done",
  detail: "",
  created_at: "2026-10-20T12:00:00Z",
  setup_done_at: "2026-10-20T12:10:00Z",
  ...o,
});

const conv = (text: string | null): ChatConversation => ({
  workspace: "loja",
  can_talk: true,
  status: { state: "idle", ahead: 0, locked: false },
  entries: text
    ? [{ id: "in-1", from: "user", channel: "panel", text, at: "2026-10-26T14:00:00Z" } as never]
    : [],
  unread: 0,
  limit: 1,
  max_files: 1,
});

const apps = (ready: boolean) => ({
  count: { mine: 2, others: 0, total: 2, limit: 100, warn: 90, near: false },
  apps: ["grand_master", "mestre"].map((role) => ({
    name: role,
    slug: role,
    role,
    class: "",
    state: ready ? "pronto" : "aguardando_criacao",
    state_label: "x",
    repos: [],
    request: "",
    can_link: false,
  })),
  requests: [],
  creation: true,
});

interface World {
  level?: string;
  caps?: string[];
  workspaces?: WorkspaceRow[];
  tables?: TableBlock[] | (() => TableBlock[]);
  requests?: ApprovalItem[];
  apps?: ReturnType<typeof apps> | "fail";
  profiles?: ProfileRow[];
  said?: string | null;
  telegram?: boolean;
}

function server(w: World = {}) {
  const level = w.level ?? "owner";
  const caps = w.caps ?? OWNER_CAPS;
  const list = w.workspaces ?? [];
  const tables = w.tables ?? [];
  const self: Partial<UserRow> = { id: ME.user.id, telegram: w.telegram ?? false };
  return fakeServer({
    "GET /api/me": () => ({
      body: {
        ...ME,
        user: { ...ME.user, level, workspaces: list.map((x) => x.slug) },
        capabilities: caps,
      },
    }),
    "GET /api/status": () => ({ body: { setup_open: false } }),
    "GET /api/transfer": () => ({ body: {} }),
    "GET /api/notices": () => ({ body: { notices: [], unread: 0 } }),
    "GET /api/chat": () => ({ body: { workspaces: [], unread: 0, limit: 1, max_files: 1 } }),
    "GET /api/approvals/count": () => ({ body: { pending: w.requests?.length ?? 0 } }),
    "GET /api/approvals": () => ({
      body: { requests: w.requests ?? [], blocking: [], pending: w.requests?.length ?? 0 },
    }),
    "GET /api/workspaces": () => ({
      body: { workspaces: list, sheets: [], can_create: level === "owner" },
    }),
    "GET /api/workspaces/loja/view": () => ({
      body: view(typeof tables === "function" ? tables() : tables),
    }),
    "GET /api/chat/loja": () => ({ body: conv(w.said === undefined ? null : w.said) }),
    "GET /api/github/apps": () =>
      w.apps === "fail"
        ? { status: 500, body: { error: { code: "internal", message: "x" } } }
        : { body: w.apps ?? apps(false) },
    "GET /api/profiles": () => ({
      body: { machine: "m1", providers: [], profiles: w.profiles ?? [], setup: true },
    }),
    "GET /api/users": () => ({ body: { users: [self], workspaces: [] } }),
    "GET /api/guild": () => ({
      body: {
        epoch: 1,
        timezone: "America/Sao_Paulo",
        single: false,
        pending: [],
        machines: [
          { id: "m1", name: "Cidadela", citadel: true, self: true, online: true, excluded: false },
          {
            id: "m2",
            name: "pc-da-sala",
            citadel: false,
            self: false,
            online: false,
            last_seen: "2026-10-26T12:41:00Z",
            excluded: false,
          },
        ],
      },
    }),
  });
}

const done = {
  apps: apps(true),
  profiles: [profile()],
  workspaces: [row()],
  said: "abra a mesa do login",
};

// CA-JORN-01
test("a new Guildmaster sees 0 of 4, the talk button off with the reason and no empty line that contradicts it", async () => {
  server({ workspaces: [] });
  renderApp("/");
  expect(await screen.findByText(/0 de 4 passos feitos/)).toBeTruthy();
  const talk = screen.getAllByRole("button", { name: "Falar com o Grão-Mestre" })[0];
  expect(talk?.hasAttribute("disabled")).toBe(true);
  expect(screen.getAllByText("Disponível depois que você criar a primeira campanha.").length).toBe(
    1,
  );
  expect(screen.queryByText(/Nada em andamento/)).toBeNull();
  expect(screen.queryByText(/Ainda não há nenhuma campanha/)).toBeNull();
});

// CA-JORN-02
test("creating the first campaign waits for the Apps and turns on with them", async () => {
  server({ apps: apps(false) });
  const first = renderApp("/");
  const step = async () => (await screen.findByText("Criar a primeira campanha")).closest("li");
  const li = await step();
  expect(li).toBeTruthy();
  const off = within(li as HTMLElement).getByRole("button");
  expect(off.hasAttribute("disabled")).toBe(true);
  expect((li as HTMLElement).textContent).toContain("Faça antes o passo 1.");
  first.unmount();

  server({ apps: apps(true) });
  renderApp("/");
  const on = await step();
  await waitFor(() =>
    expect(within(on as HTMLElement).getByRole("link", { name: "Criar campanha" })).toBeTruthy(),
  );
  expect((on as HTMLElement).textContent).not.toContain("Faça antes");
});

// CA-JORN-03
test("with everything done the card is not there, and still is not after reloading", async () => {
  server(done);
  const first = renderApp("/");
  await screen.findByRole("heading", { name: "Início" });
  await waitFor(() => expect(screen.queryByText(/passos feitos/)).toBeNull());
  expect(screen.queryByRole("heading", { name: "Primeiros passos" })).toBeNull();
  first.unmount();
  const stored = JSON.stringify({ ...localStorage });
  server(done);
  renderApp("/");
  await screen.findByRole("heading", { name: "Início" });
  await waitFor(() =>
    expect(screen.queryByRole("heading", { name: "Primeiros passos" })).toBeNull(),
  );
  // nothing was kept for the steps: the storage is as it was
  expect(JSON.stringify({ ...localStorage })).toBe(stored);
});

// CA-JORN-04
test("the tables and the request show with their links, and a new table appears without reloading", async () => {
  let tables = [
    table({ id: "1-a", epic_title: "Mesa A" }),
    table({ id: "2-b", epic_title: "Mesa B", state: "in_review" }),
    table({ id: "3-c", epic_title: "Mesa C" }),
  ];
  server({ ...done, tables: () => tables, requests: [request()] });
  renderApp("/");
  const now = await screen.findByRole("region", { name: "Acontecendo agora" });
  const link = await within(now).findByRole("link", {
    name: "Abrir a mesa Mesa A da campanha loja",
  });
  expect(link.getAttribute("href")).toBe("/workspaces/loja/mesas/1-a");
  expect(within(now).getAllByRole("link").length).toBe(3);
  expect(now.textContent).toContain("3 personagens (Cidadela)");
  const waiting = await screen.findByRole("region", { name: "Esperam por você" });
  const req = within(waiting).getByRole("link", { name: /Decidir o pedido 5/ });
  expect(req.getAttribute("href")).toContain("/aprovacoes");
  expect(req.getAttribute("href")).toContain("pedido=5");
  expect(screen.queryByText(/Ainda não há nenhuma campanha/)).toBeNull();

  const sock = openSocket();
  expect(sock.subscribed()).toContain("workspaces:all");
  tables = [...tables, table({ id: "4-d", epic_title: "Mesa D" })];
  act(() => emit(sock, "workspaces:all", "changed", {}));
  expect(
    await screen.findByRole("link", { name: "Abrir a mesa Mesa D da campanha loja" }),
  ).toBeTruthy();
});

test("closed tables with a merged PR show as recent deliveries, not as work", async () => {
  server({
    ...done,
    tables: [
      table({ id: "9-x", epic_title: "Página de contato", state: "closed", pr: 10 }),
      table({ id: "1-a", epic_title: "Mesa A" }),
    ],
  });
  renderApp("/");
  const del = await screen.findByRole("region", { name: "Entregas recentes" });
  expect(del.textContent).toContain("Página de contato");
  expect(del.textContent).toContain("PR #10 mesclado");
  const now = screen.getByRole("region", { name: "Acontecendo agora" });
  expect(now.textContent).not.toContain("Página de contato");
});

test("with no table the empty line says what to do, and the Guild health shows who is down", async () => {
  server({ ...done, tables: [] });
  renderApp("/");
  expect(await screen.findByText("Nada em andamento.")).toBeTruthy();
  const health = await screen.findByRole("complementary", { name: "Saúde da Guilda" });
  expect(health.textContent).toContain("Cidadela");
  expect(health.textContent).toMatch(/pc-da-sala[\s\S]*sem sinal desde \d{2}:\d{2}/);
});

// CA-JORN-05
test("an Administrator sees no first steps, and an Observer sees no requests either", async () => {
  server({
    ...done,
    level: "administrator",
    caps: ["status", "talk_to_grand_master", "decide_approvals"],
    requests: [request()],
  });
  const admin = renderApp("/");
  await screen.findByRole("region", { name: "Esperam por você" });
  expect(screen.queryByRole("heading", { name: "Primeiros passos" })).toBeNull();
  admin.unmount();

  const s = server({ ...done, level: "observer", caps: ["status"], requests: [request()] });
  renderApp("/");
  await screen.findByRole("region", { name: "Acontecendo agora" });
  expect(screen.queryByRole("heading", { name: "Primeiros passos" })).toBeNull();
  expect(screen.queryByRole("region", { name: "Esperam por você" })).toBeNull();
  expect(
    s.calls.some((c) => c.path.startsWith("/api/approvals?") || c.path === "/api/approvals"),
  ).toBe(false);
  for (const p of ["/api/github/apps", "/api/profiles", "/api/users", "/api/guild"])
    expect(s.calls.some((c) => c.path === p)).toBe(false);
});

test("a user with no campaign is told to ask the Guildmaster", async () => {
  server({ level: "approver", caps: ["status", "talk_to_grand_master"], workspaces: [] });
  renderApp("/");
  expect(
    await screen.findByText("Peça ao Senhor da Guilda para dar acesso a uma campanha."),
  ).toBeTruthy();
});

// CA-JORN-06
test("with the four steps done only the optional Telegram line shows, and Not now hides it in this browser", async () => {
  server(done);
  const user = userEvent.setup({ delay: null });
  const first = renderApp("/");
  const line = await screen.findByText(/Opcional: parear o Telegram/);
  expect(line).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Primeiros passos" })).toBeNull();
  await user.click(screen.getByRole("button", { name: "Agora não" }));
  expect(screen.queryByText(/Opcional: parear o Telegram/)).toBeNull();
  first.unmount();

  server(done);
  renderApp("/");
  await screen.findByRole("region", { name: "Acontecendo agora" });
  expect(screen.queryByText(/Opcional: parear o Telegram/)).toBeNull();
});

// CA-JORN-08
test("a step whose read fails says it does not know and offers to try again, never done", async () => {
  server({ ...done, apps: "fail" });
  const user = userEvent.setup({ delay: null });
  renderApp("/");
  const li = (await screen.findByText("Conectar o GitHub")).closest("li") as HTMLElement;
  expect(li.dataset.status).toBe("unknown");
  expect(li.textContent).not.toContain("Feito");
  expect(li.textContent).toContain("Não sei");
  const before = fetch as unknown as { mock: { calls: unknown[][] } };
  const n = before.mock.calls.filter((c) => c[0] === "/api/github/apps").length;
  await user.click(within(li).getByRole("button", { name: "Tentar de novo" }));
  await waitFor(() =>
    expect(before.mock.calls.filter((c) => c[0] === "/api/github/apps").length).toBeGreaterThan(n),
  );
});
