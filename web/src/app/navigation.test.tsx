import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { TableBlock, WorkspaceRow, WorkspaceView } from "../gen/panel";
import { installFakeSocket } from "../test-socket";
import { fakeServer, ME, renderApp } from "../test-utils";

beforeEach(() => installFakeSocket());
afterEach(() => vi.unstubAllGlobals());

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
  party: [],
  party_size: 0,
  waits: [],
  stalled: false,
  quiet_seconds: 0,
  pr: 0,
  ...o,
});

const view = (slug: string, tables: TableBlock[]): WorkspaceView => ({
  workspace: row({ slug, repo: `acme/${slug}` }),
  tables,
  layout: [],
  can_pause: true,
  can_type: true,
});

const as = (level: string, caps: string[], extra: Record<string, unknown> = {}) => ({
  "GET /api/me": () => ({
    body: {
      ...ME,
      user: { ...ME.user, level, workspaces: ["loja", "blog"], ...extra },
      capabilities: caps,
    },
  }),
});

const OWNER_CAPS = [
  "status",
  "decide_approvals",
  "connect_own_github",
  "manage_users",
  "bases_vault_policy",
  "activate_classes",
  "configure_workspace",
  "approve_workspace_cls",
  "account_profiles",
  "manage_machines",
  "talk_to_grand_master",
];

const base = (extra: Record<string, unknown> = {}) => ({
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/transfer": () => ({ body: {} }),
  "GET /api/notices": () => ({ body: { notices: [], unread: 0 } }),
  "GET /api/chat": () => ({
    body: {
      workspaces: [
        { slug: "loja", can_talk: true, unread: 0 },
        { slug: "blog", can_talk: true, unread: 0 },
      ],
      unread: 0,
      limit: 1,
      max_files: 1,
    },
  }),
  "GET /api/approvals/count": () => ({ body: { pending: 2 } }),
  "GET /api/workspaces": () => ({
    body: {
      workspaces: [row(), row({ slug: "blog", repo: "acme/blog" })],
      sheets: [],
      can_create: true,
    },
  }),
  "GET /api/workspaces/loja/view": () => ({ body: view("loja", [table()]) }),
  "GET /api/workspaces/blog/view": () => ({ body: view("blog", []) }),
  "GET /api/workspaces/loja/tables/42-login/canvas": () => ({ status: 404, body: {} }),
  "PUT /api/me/workspace": (c: { body: unknown }) => ({
    body: {
      ...ME,
      user: {
        ...ME.user,
        level: "owner",
        last_workspace: (c.body as { workspace: string }).workspace,
      },
    },
  }),
  ...as("owner", OWNER_CAPS),
  ...extra,
});

const mainNav = () => screen.getByRole("navigation", { name: "Navegação principal" });
const current = () =>
  within(mainNav())
    .getAllByRole("link")
    .filter((l) => l.getAttribute("aria-current") === "page")
    .map((l) => l.textContent?.replace(/\d+$/, ""));

// CA-JORN-09
test("the menu has four items, and the one of the section is marked on every screen of it", async () => {
  fakeServer(
    base({
      "GET /api/characters/term-1/terminal": () => ({
        status: 404,
        body: { error: { code: "not_found", message: "x" } },
      }),
    }),
  );
  const { router } = renderApp("/");
  await screen.findByRole("navigation", { name: "Navegação principal" });
  const links = within(mainNav()).getAllByRole("link");
  expect(links.map((l) => l.textContent?.replace(/\d+$/, ""))).toEqual([
    "Início",
    "Aprovações",
    "Campanhas",
    "Configurações",
  ]);
  expect(current()).toEqual(["Início"]);
  const expected: [string, string][] = [
    ["/aprovacoes", "Aprovações"],
    ["/workspaces", "Campanhas"],
    ["/workspaces/loja", "Campanhas"],
    ["/workspaces/loja/mesas/42-login", "Campanhas"],
    ["/terminal/term-1", "Campanhas"],
    ["/configuracoes", "Configurações"],
    ["/decretos", "Configurações"],
    ["/consumo", "Configurações"],
    ["/sessoes", "Configurações"],
  ];
  for (const [path, item] of expected) {
    router.history.push(path);
    await waitFor(() => expect(router.state.location.pathname).toBe(path.split("?")[0]));
    await waitFor(() => expect(current(), path).toEqual([item]));
  }
}, 20_000);

// CA-JORN-09: the menu shows only what the level does
test("who does not decide approvals has three items and no counter", async () => {
  fakeServer(base(as("observer", ["status"])));
  renderApp("/");
  await screen.findByRole("navigation", { name: "Navegação principal" });
  expect(
    within(mainNav())
      .getAllByRole("link")
      .map((l) => l.textContent),
  ).toEqual(["Início", "Campanhas", "Configurações"]);
});

test("the item Aprovações carries the number of pending requests", async () => {
  fakeServer(base());
  renderApp("/");
  const link = await screen.findByRole("link", { name: "Aprovações pendentes: 2" });
  expect(link.textContent).toContain("2");
});

// CA-JORN-11
test("the top bar has the brand, the bell, the Grão-Mestre and the account, and the menu is a bar of four tabs", async () => {
  fakeServer(base());
  renderApp("/");
  const nav = await screen.findByRole("navigation", { name: "Navegação principal" });
  const bar = screen.getByRole("banner");
  // one row: nothing wraps, no selector of campaign, no counter of approvals
  expect(bar.className).not.toMatch(/flex-wrap/);
  expect(bar.querySelector("select")).toBeNull();
  const names = [...bar.querySelectorAll("a, button")].map((e) => e.getAttribute("aria-label"));
  expect(names).toEqual([
    "Mestre, ir para o Início",
    "Avisos",
    "Abrir a conversa com o Grão-Mestre",
    "Menu da conta",
  ]);
  // the label of the Grão-Mestre only shows from the computer width: on the phone it is the icon
  expect(within(bar).getByText("Grão-Mestre").className).toMatch(/hidden md:inline/);
  // the menu is fixed at the bottom of the phone, four equal tabs
  expect(nav.className).toMatch(/fixed/);
  expect(nav.className).toMatch(/bottom-0/);
  expect(within(nav).getAllByRole("link")).toHaveLength(4);
  for (const l of within(nav).getAllByRole("link")) expect(l.className).toMatch(/min-h-14/);
});

// CA-JORN-10
test("the trail of a table reads Campanhas › campanha › mesa, and the first two levels lead back", async () => {
  fakeServer(base());
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/workspaces/loja/mesas/42-login");
  const trail = await screen.findByRole("navigation", { name: "Você está em" });
  await within(trail).findByText("Login com Google");
  const items = within(trail).getAllByRole("listitem");
  expect(items.map((i) => i.textContent?.replace(/[▾›]/g, "").trim())).toEqual([
    "Campanhas",
    "loja",
    "Login com Google",
  ]);
  expect(within(trail).getByText("Login com Google").getAttribute("aria-current")).toBe("page");
  expect(current()).toEqual(["Campanhas"]);
  // the campaign opens the switch of campaigns
  await user.click(within(trail).getByRole("button", { name: /Campanha loja/ }));
  expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual([
    "loja✓",
    "blog",
    "Ver todas",
  ]);
  await user.keyboard("{Escape}");
  // the first level goes back to the list
  await user.click(within(trail).getByRole("link", { name: "Campanhas" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/workspaces"));
});

// CA-CANVAS-12
test("the canvas of the table has the trail of the campaign and marks Campanhas in the menu", async () => {
  fakeServer(base());
  renderApp("/workspaces/loja/mesas/42-login");
  const trail = await screen.findByRole("navigation", { name: "Você está em" });
  await within(trail).findByText("Login com Google");
  expect(within(trail).getByRole("link", { name: "Campanhas" })).toBeTruthy();
  expect(within(trail).getByRole("button", { name: /Campanha loja/ })).toBeTruthy();
  expect(current()).toEqual(["Campanhas"]);
});

// CA-VWS-10
test("choosing the campaign you are in does nothing, and another one opens it", async () => {
  fakeServer(base());
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/workspaces/loja");
  const trail = await screen.findByRole("navigation", { name: "Você está em" });
  await user.click(within(trail).getByRole("button", { name: /Campanha loja/ }));
  await user.click(await screen.findByRole("menuitem", { name: /^loja/ }));
  expect(router.state.location.pathname).toBe("/workspaces/loja");
  await user.click(within(trail).getByRole("button", { name: /Campanha loja/ }));
  await user.click(await screen.findByRole("menuitem", { name: "blog" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/workspaces/blog"));
});

// CA-ADM-16
test("Configurações has four groups with a sentence per entry, and every screen is two clicks away", async () => {
  fakeServer(base());
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/configuracoes");
  const groups = await screen.findAllByRole("heading", { level: 2 });
  expect(groups.map((g) => g.textContent)).toEqual([
    "Conta",
    "Pessoas e acesso",
    "Infraestrutura",
    "Regras e consumo",
  ]);
  const index = screen.getByRole("main");
  const entries = within(index).getAllByRole("link");
  expect(entries).toHaveLength(18);
  // each entry says what it does
  for (const e of entries) expect(e.querySelectorAll("span").length).toBeGreaterThan(0);
  const decrees = within(index).getByRole("link", { name: /Decretos/ });
  expect(decrees.textContent).toContain("Permissões dadas de uma vez");
  await user.click(decrees);
  await waitFor(() => expect(router.state.location.pathname).toBe("/decretos"));
  const trail = await screen.findByRole("navigation", { name: "Você está em" });
  expect(
    within(trail)
      .getAllByRole("listitem")
      .map((i) => i.textContent?.replace("›", "").trim()),
  ).toEqual(["Configurações", "Regras e consumo", "Decretos"]);
  await user.click(within(trail).getByRole("link", { name: "Configurações" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/configuracoes"));
});

// CA-ADM-16
test("an Approver sees only the groups and entries the level reaches, and the old address opens the same screen", async () => {
  fakeServer(
    base({
      ...as("approver", ["status", "decide_approvals"]),
      "GET /api/decrees": () => ({ body: { workspaces: [] } }),
    }),
  );
  const { router } = renderApp("/configuracoes");
  await screen.findByRole("heading", { name: "Configurações", level: 1 });
  const index = screen.getByRole("main");
  expect(
    within(index)
      .getAllByRole("heading", { level: 2 })
      .map((h) => h.textContent),
  ).toEqual(["Conta", "Regras e consumo"]);
  const names = within(index)
    .getAllByRole("link")
    .map((l) => l.querySelector("strong")?.textContent);
  expect(names).toEqual([
    "Minhas sessões",
    "Trocar a senha",
    "Decretos",
    "Delegações",
    "Supervisão da cadeia",
    "Teto da campanha",
    "Consumo de uso",
  ]);
  router.history.push("/decretos");
  expect(await screen.findByRole("heading", { name: "Decretos", level: 1 })).toBeTruthy();
});

// CA-VWS-10
test("an active campaign has Abrir and Conversar, a campaign in preparation has Abrir off with the stage", async () => {
  fakeServer(
    base({
      "GET /api/workspaces": () => ({
        body: {
          workspaces: [
            row(),
            row({
              slug: "blog",
              repo: "acme/blog",
              state: "preparing",
              stages: [
                { key: "apps", state: "done" },
                { key: "mirror", state: "current" },
              ],
            }),
          ],
          sheets: [],
          can_create: false,
        },
      }),
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/workspaces");
  const loja = await screen.findByRole("article", { name: "loja" });
  expect(
    within(loja).getByRole("link", { name: "Abrir a campanha loja" }).getAttribute("href"),
  ).toBe("/workspaces/loja");
  expect(within(loja).getByRole("button", { name: /Conversar/ })).toBeTruthy();
  const blog = screen.getByRole("article", { name: "blog" });
  const open = within(blog).getByRole("button", { name: "Abrir" }) as HTMLButtonElement;
  expect(open.disabled).toBe(true);
  expect(blog.textContent).toContain("agora: Espelho e clone principal");
  expect(within(blog).queryByRole("button", { name: /Conversar/ })).toBeNull();
  await user.click(within(loja).getByRole("link", { name: "Abrir a campanha loja" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/workspaces/loja"));
});

test("a list with no campaign says what to do next, by level", async () => {
  fakeServer(
    base({
      "GET /api/workspaces": () => ({ body: { workspaces: [], sheets: [], can_create: false } }),
    }),
  );
  renderApp("/workspaces");
  expect(await screen.findByText("Você ainda não tem campanhas.")).toBeTruthy();
  expect(screen.getByText(/Senhor da Guilda para dar acesso/)).toBeTruthy();
});

// CA-VWS-11
test("the conversation talks about the last campaign opened, follows the campaign of the trail and has no selector of its own", async () => {
  const asked: string[] = [];
  const conv = (slug: string) => () => {
    asked.push(slug);
    return {
      body: {
        workspace: slug,
        can_talk: true,
        status: { state: "idle", ahead: 0, locked: false },
        entries: [],
        unread: 0,
        limit: 1,
        max_files: 1,
      },
    };
  };
  fakeServer(
    base({
      ...as("owner", OWNER_CAPS, { last_workspace: "loja" }),
      "GET /api/chat/loja": conv("loja"),
      "GET /api/chat/blog": conv("blog"),
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/");
  await user.click(
    await screen.findByRole("button", { name: "Abrir a conversa com o Grão-Mestre" }),
  );
  const panel = await screen.findByRole("complementary", { name: "Conversa com o Grão-Mestre" });
  expect(await within(panel).findByText("loja")).toBeTruthy();
  expect(within(panel).queryByRole("combobox")).toBeNull();
  router.history.push("/workspaces/blog");
  expect(await within(panel).findByText("blog")).toBeTruthy();
  expect(asked).toEqual(["loja", "blog"]);
});

test("Conversar makes the campaign the current one and opens its conversation", async () => {
  const puts: unknown[] = [];
  fakeServer(
    base({
      ...as("owner", OWNER_CAPS, { last_workspace: "loja" }),
      "PUT /api/me/workspace": (c: { body: unknown }) => {
        puts.push(c.body);
        return {
          body: {
            ...ME,
            user: { ...ME.user, workspaces: ["loja", "blog"], last_workspace: "blog" },
            capabilities: OWNER_CAPS,
          },
        };
      },
      "GET /api/chat/blog": () => ({
        body: {
          workspace: "blog",
          can_talk: true,
          status: { state: "idle", ahead: 0, locked: false },
          entries: [],
          unread: 0,
          limit: 1,
          max_files: 1,
        },
      }),
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces");
  await user.click(
    await screen.findByRole("button", { name: "Conversar com o Grão-Mestre de blog" }),
  );
  const panel = await screen.findByRole("complementary", { name: "Conversa com o Grão-Mestre" });
  expect(await within(panel).findByText("blog")).toBeTruthy();
  expect(puts).toEqual([{ workspace: "blog" }]);
});

// CA-JORN-07
test("the progress message of the conversation lists the table with a link that leads to it", async () => {
  fakeServer(
    base({
      ...as("owner", OWNER_CAPS, { last_workspace: "loja" }),
      "GET /api/chat/loja": () => ({
        body: {
          workspace: "loja",
          can_talk: true,
          status: { state: "idle", ahead: 0, locked: false },
          entries: [],
          unread: 0,
          limit: 1,
          max_files: 1,
          progress: { lines: ["1 mesa em andamento"], updated_at: "2026-10-27T10:00:00Z" },
        },
      }),
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/");
  await user.click(
    await screen.findByRole("button", { name: "Abrir a conversa com o Grão-Mestre" }),
  );
  const panel = await screen.findByRole("complementary", { name: "Conversa com o Grão-Mestre" });
  const link = await within(panel).findByRole("link", { name: "Login com Google" });
  expect(link.getAttribute("href")).toBe("/workspaces/loja/mesas/42-login");
  await user.click(link);
  await waitFor(() =>
    expect(router.state.location.pathname).toBe("/workspaces/loja/mesas/42-login"),
  );
  // the panel closes so the table is not hidden behind it
  await waitFor(() => expect(screen.queryByRole("complementary")).toBeNull());
});

test("the account menu has my account, the language and the exit", async () => {
  fakeServer(base());
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/");
  await user.click(await screen.findByRole("button", { name: "Menu da conta" }));
  expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual([
    "Minha conta",
    "Sair",
  ]);
  expect(
    screen.getByRole("menuitemradio", { name: "Português" }).getAttribute("aria-checked"),
  ).toBe("true");
  await user.click(screen.getByRole("menuitem", { name: "Minha conta" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/sessoes"));
});

// CA-VWS-11
test("a campaign with no conversation is said so and never swapped for another one", async () => {
  fakeServer(
    base({
      ...as("owner", OWNER_CAPS, { last_workspace: "blog" }),
      "GET /api/chat": () => ({
        body: {
          workspaces: [{ slug: "loja", can_talk: true, unread: 0 }],
          unread: 0,
          limit: 1,
          max_files: 1,
        },
      }),
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  await user.click(
    await screen.findByRole("button", { name: "Abrir a conversa com o Grão-Mestre" }),
  );
  const panel = await screen.findByRole("complementary", { name: "Conversa com o Grão-Mestre" });
  expect(await within(panel).findByText(/A campanha blog não tem conversa agora/)).toBeTruthy();
  expect(within(panel).queryByRole("textbox")).toBeNull();
});
