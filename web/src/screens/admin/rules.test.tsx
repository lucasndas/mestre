import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { fakeServer, fill, ME, renderApp } from "../../test-utils";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const caps = (...c: string[]) => ({
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/me": () => ({ body: { ...ME, capabilities: c } }),
  "GET /api/transfer": () => ({ body: {} }),
});
const OWNER = caps("status", "one_off_exception", "bases_vault_policy", "configure_workspace");

const decree = (o: Record<string, unknown> = {}) => ({
  number: 7,
  kind: "rule",
  state: "active",
  workspace: "loja",
  mesa: "",
  scope: "",
  effect: "permission",
  summary: "libera comandos npm test* para a classe dev",
  text: "dev pode rodar npm test sem perguntar",
  author: "u1",
  author_name: "Maria Souza",
  level: "owner",
  created_at: "2026-10-20T12:00:00Z",
  frozen: false,
  uses: [{ at: "2026-10-25T10:00:00Z", character: "dev-1", action: "npm test" }],
  use_count: 1,
  can_revoke: true,
  revoke_needs_pin: false,
  ...o,
});

test("the decrees screen shows who ordered what, until when and what it freed", async () => {
  fakeServer({
    ...OWNER,
    "GET /api/decrees": () => ({
      body: {
        workspaces: [{ workspace: "loja", decrees: [decree({ until: "2026-11-20T21:00:00Z" })] }],
      },
    }),
  });
  renderApp("/decretos");
  expect(await screen.findByRole("heading", { name: "Decretos", level: 1 })).toBeTruthy();
  expect(await screen.findByText("libera comandos npm test* para a classe dev")).toBeTruthy();
  expect(screen.getByText(/dev pode rodar npm test sem perguntar/)).toBeTruthy();
  expect(screen.getByText("Maria Souza · Senhor da Guilda")).toBeTruthy();
  expect(screen.getByText(/até 20\/11\/2026/)).toBeTruthy();
  expect(screen.getByText(/dev-1 · npm test/)).toBeTruthy();
});

test("revoking a decree asks again, then revokes and says so", async () => {
  let revoked = false;
  const { calls } = fakeServer({
    ...OWNER,
    "GET /api/decrees": () => ({
      body: { workspaces: [{ workspace: "loja", decrees: revoked ? [] : [decree()] }] },
    }),
    "POST /api/decrees/7/revoke": () => {
      revoked = true;
      return { status: 204 };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/decretos");
  await user.click(await screen.findByRole("button", { name: "Revogar" }));
  // nothing is revoked by the first press
  expect(calls.some((c) => c.path === "/api/decrees/7/revoke")).toBe(false);
  await user.click(screen.getByRole("button", { name: "Sim, revogar" }));
  expect(await screen.findByText("Decreto 7 revogado.")).toBeTruthy();
  const post = calls.find((c) => c.path === "/api/decrees/7/revoke");
  expect(post?.headers["X-Mestre-Pin"]).toBeUndefined();
  await waitFor(() => expect(screen.getByText("Para que serve um decreto?")).toBeTruthy());
});

test("a decree that restricted something is revoked with the PIN in the very request", async () => {
  const { calls } = fakeServer({
    ...OWNER,
    "GET /api/decrees": () => ({
      body: { workspaces: [{ workspace: "loja", decrees: [decree({ revoke_needs_pin: true })] }] },
    }),
    "POST /api/decrees/7/revoke": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/decretos");
  await user.click(await screen.findByRole("button", { name: "Revogar" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Revogar" }));
  await screen.findByText("Decreto 7 revogado.");
  expect(calls.find((c) => c.path === "/api/decrees/7/revoke")?.headers["X-Mestre-Pin"]).toBe(
    "123456",
  );
});

test("a new decree is only a proposal until the author confirms it with the PIN", async () => {
  const { calls } = fakeServer({
    ...OWNER,
    "GET /api/decrees": () => ({ body: { workspaces: [{ workspace: "loja", decrees: [] }] } }),
    "GET /api/rules/workspaces": () => ({ body: { workspaces: ["loja"] } }),
    "POST /api/decrees": () => ({
      status: 201,
      body: { id: "p1", text: "Entendi: liberar npm test*. Confirma?", order: "x", pin: true },
    }),
    "POST /api/decrees/proposals/p1/confirm": () => ({
      status: 201,
      body: { number: 8, state: "active" },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/decretos");
  await user.click(await screen.findByRole("button", { name: "Novo decreto" }));
  await fill(user, screen.getByLabelText("Nome"), "dev");
  await fill(user, screen.getByLabelText("Padrão"), "npm test*");
  await fill(user, screen.getByLabelText("A ordem, com as suas palavras"), "dev roda npm test");
  await user.click(screen.getByRole("button", { name: "Ver o que o Mestre entendeu" }));
  expect(await screen.findByText("Entendi: liberar npm test*. Confirma?")).toBeTruthy();
  expect(screen.getByText(/Isto ainda não é um decreto/)).toBeTruthy();
  const proposal = calls.find((c) => c.method === "POST" && c.path === "/api/decrees");
  expect(proposal?.body).toMatchObject({
    workspace: "loja",
    kind: "rule",
    effect: "permission",
    target_kind: "class",
    target_name: "dev",
    field: "commands",
    side: "allow",
    value: "npm test*",
    text: "dev roda npm test",
  });
  expect(calls.some((c) => c.path.includes("/confirm"))).toBe(false);
  await user.click(screen.getByRole("button", { name: "Confirmar decreto" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Confirmar decreto" }));
  await screen.findByText("Decreto feito.");
  expect(calls.find((c) => c.path.includes("/confirm"))?.headers["X-Mestre-Pin"]).toBe("123456");
});

const delegations = {
  workspaces: [
    {
      workspace: "loja",
      delegations: [
        {
          ...decree({
            number: 3,
            effect: "delegation",
            summary: "o Grão-Mestre decide envio de workflows",
            mesa: "42-login",
          }),
          type: "envio_workflows",
          decisions: 4,
        },
      ],
      subdelegations: [
        {
          id: 1,
          workspace: "loja",
          mesa: "42-login",
          type: "envio_workflows",
          grantor: "gm",
          grantee: "mesa-42",
          origin: 3,
          reason: "testes",
          created_at: "2026-10-26T09:00:00Z",
          until: "2026-10-30T09:00:00Z",
          active: true,
          decisions: 2,
        },
      ],
      rules: [
        {
          id: 5,
          workspace: "loja",
          table: "42-login",
          class: "dev",
          state: "active",
          risk: "medio",
          level: "mesa",
          author: "mesa-42",
          covers: "npm test*",
          created_at: "2026-10-26T09:00:00Z",
          until: "2026-10-26T15:00:00Z",
          decisions: 1,
        },
        {
          id: 6,
          workspace: "loja",
          table: "42-login",
          class: "dev",
          state: "active",
          risk: "baixo",
          level: "mesa",
          author: "mesa-42",
          covers: "make lint",
          created_at: "2026-10-26T09:00:00Z",
          decisions: 0,
        },
      ],
    },
  ],
  can_revoke_all: false,
  can_revoke: true,
};

test("an Administrator sees the four things and revokes a rule, with no button to revoke all", async () => {
  let revoked = false;
  const { calls } = fakeServer({
    ...caps("status", "revoke_temporary"),
    "GET /api/delegations": () => ({ body: delegations }),
    "POST /api/table-rules/5/revoke": () => {
      revoked = true;
      return { status: 204 };
    },
  });
  void revoked;
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/delegacoes");
  expect(await screen.findByText("o Grão-Mestre decide envio de workflows")).toBeTruthy();
  expect(screen.getByText("Subdelegação 1")).toBeTruthy();
  expect(screen.getByText("Regra temporária 5")).toBeTruthy();
  expect(screen.getByText("Regra temporária 6")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Revogar todas as delegações" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Renovar" })).toBeNull();
  const card = screen.getByText("Regra temporária 5").closest("li") as HTMLElement;
  await user.click(card.querySelector("button") as HTMLButtonElement);
  await user.click(screen.getByRole("button", { name: "Sim, revogar" }));
  await waitFor(() => expect(calls.some((c) => c.path === "/api/table-rules/5/revoke")).toBe(true));
});

test("the Owner revokes every delegation only after a second confirmation", async () => {
  const { calls } = fakeServer({
    ...caps("status", "delegate_high", "revoke_temporary"),
    "GET /api/delegations": () => ({ body: { ...delegations, can_revoke_all: true } }),
    "POST /api/delegations/revoke-all": () => ({
      body: { delegations: 1, subdelegations: 1, rules: 2, rules_by_workspace: { loja: 2 } },
    }),
    "POST /api/decrees/3/renew": () => ({
      status: 201,
      body: { id: "r1", text: "Renovar a delegação 3. Confirma?", order: "x", pin: true },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/delegacoes");
  await user.click(await screen.findByRole("button", { name: "Revogar todas as delegações" }));
  expect(calls.some((c) => c.path === "/api/delegations/revoke-all")).toBe(false);
  expect(screen.getByText(/Isto encerra 4 delegação\(ões\)/)).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Sim, revogar tudo" }));
  expect(
    await screen.findByText(
      "Revogado: 1 delegação(ões), 1 subdelegação(ões) e 2 regra(s) temporária(s).",
    ),
  ).toBeTruthy();
  // renewing opens a proposal; it is not a decree yet
  await user.click(screen.getByRole("button", { name: "Renovar" }));
  expect(await screen.findByText("Renovar a delegação 3. Confirma?")).toBeTruthy();
});

const chainBody = (over: Record<string, unknown> = {}) => ({
  workspaces: [
    {
      workspace: "loja",
      queues: [{ level: "people", open: 1, oldest_seconds: 300, avg_wait_seconds: 0 }],
      tables: [
        {
          table: "42-login",
          queues: [
            {
              level: "mesa",
              table: "42-login",
              open: 3,
              oldest_seconds: 2400,
              avg_wait_seconds: 600,
            },
          ],
          can_resume: false,
        },
        {
          table: "43-busca",
          queues: [],
          pause: {
            by: "gm-loja",
            kind: "character",
            reason: "muitas falhas",
            since: "2026-10-26T09:00:00Z",
          },
          can_resume: false,
        },
      ],
    },
  ],
  can_pause: true,
  ...over,
});

test("an Approver pauses the autonomy of a table with a reason and sees the queue", async () => {
  let paused = false;
  const { calls } = fakeServer({
    ...caps("status", "pause_buttons"),
    "GET /api/chain": () => {
      const b = chainBody();
      const first = b.workspaces[0]?.tables[0];
      if (paused && b.workspaces[0] && first) {
        b.workspaces[0].tables[0] = {
          table: "42-login",
          queues: first.queues,
          pause: {
            by: "user:u2",
            by_name: "Carla",
            kind: "person",
            reason: "muitas falhas",
            since: "2026-10-26T09:05:00Z",
          },
          can_resume: true,
        } as never;
      }
      return { body: b };
    },
    "POST /api/chain/loja/tables/42-login/pause": () => {
      paused = true;
      return { status: 204 };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/cadeia");
  const card = await screen.findByRole("article", { name: "42-login" });
  expect(card.textContent).toContain("3");
  expect(card.textContent).toContain("40 min");
  const pause = card.querySelector("button") as HTMLButtonElement;
  expect(pause.disabled).toBe(true); // a reason first
  await fill(user, within(card).getByLabelText("Motivo"), "muitas falhas");
  await user.click(within(card).getByRole("button", { name: "Pausar autonomia" }));
  expect(await screen.findByText("Autonomia da mesa 42-login pausada.")).toBeTruthy();
  expect(calls.find((c) => c.path.endsWith("/pause"))?.body).toEqual({ reason: "muitas falhas" });
  const after = await screen.findByRole("article", { name: "42-login" });
  expect(after.textContent).toContain("Pausada por Carla");
  expect(screen.getAllByRole("button", { name: "Retomar" }).length).toBe(1);
});

test("the pause of the Grand Master offers no resume, and an Observer has no buttons", async () => {
  fakeServer({
    ...caps("status", "pause_buttons"),
    "GET /api/chain": () => ({ body: chainBody() }),
  });
  renderApp("/cadeia");
  const gm = await screen.findByRole("article", { name: "43-busca" });
  expect(gm.textContent).toContain("Pausada por o Grão-Mestre");
  expect(gm.textContent).toContain("Só o Grão-Mestre retoma esta pausa.");
  expect(gm.querySelector("button")).toBeNull();
});

test("an Observer reads the chain and sees no button", async () => {
  fakeServer({
    ...caps("status"),
    "GET /api/chain": () => ({ body: chainBody({ can_pause: false }) }),
  });
  renderApp("/cadeia");
  await screen.findByRole("article", { name: "42-login" });
  expect(screen.queryByRole("button", { name: "Pausar autonomia" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Retomar" })).toBeNull();
});

test("widening the ceiling opens a request that the author confirms; the file is only read", async () => {
  const { calls } = fakeServer({
    ...caps("status", "raise_ceiling"),
    "GET /api/rules/workspaces": () => ({ body: { workspaces: ["loja"] } }),
    "GET /api/workspaces/loja/ceiling": () => ({
      body: {
        workspace: "loja",
        present: true,
        valid: true,
        text: "version: 1\n",
        widenings: [],
        can_widen: true,
      },
    }),
    "POST /api/workspaces/loja/ceiling/widen": () => ({
      status: 201,
      body: { id: "w1", text: "Entendi: ampliar o teto. Confirma?", order: "x", pin: true },
    }),
    "POST /api/decrees/proposals/w1/confirm": () => ({
      status: 201,
      body: { number: 9, state: "active" },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/teto");
  expect(await screen.findByText(/version: 1/)).toBeTruthy();
  expect(screen.queryByRole("textbox")).toBeNull(); // no way to edit the file here
  await user.click(screen.getByRole("button", { name: "Pedir um teto maior" }));
  await fill(user, screen.getByLabelText("Padrão"), "registry.npmjs.org");
  await fill(user, screen.getByLabelText("A ordem, com as suas palavras"), "libere o npm");
  await user.click(screen.getByRole("button", { name: "Abrir o pedido de decreto" }));
  expect(await screen.findByText("Entendi: ampliar o teto. Confirma?")).toBeTruthy();
  expect(calls.find((c) => c.path.endsWith("/widen"))?.body).toEqual({
    field: "domains",
    value: "registry.npmjs.org",
    text: "libere o npm",
  });
  expect(calls.some((c) => c.path.includes("/confirm"))).toBe(false); // the ceiling does not move before the decree
  await user.click(screen.getByRole("button", { name: "Confirmar decreto" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Confirmar decreto" }));
  expect(await screen.findByText("Teto ampliado pelo decreto.")).toBeTruthy();
});

test("an invalid policy shows the line, says nothing was saved and keeps the text", async () => {
  const { calls } = fakeServer({
    ...OWNER,
    "GET /api/rules/workspaces": () => ({ body: { workspaces: ["loja"] } }),
    "GET /api/workspaces/loja/policy": () => ({
      body: {
        workspace: "loja",
        text: "versao: 1\nexpiracao: 24h\n",
        version: "v1",
        default: false,
      },
    }),
    "PUT /api/workspaces/loja/policy": () => ({
      status: 422,
      body: {
        error: {
          code: "invalid_policy",
          message: "x",
          params: { line: 2, reason: "expiracao: invalido" },
        },
      },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/politica");
  const box = (await screen.findByLabelText("politica-de-aprovacao.yaml")) as HTMLTextAreaElement;
  const save = screen.getByRole("button", { name: "Salvar política" }) as HTMLButtonElement;
  expect(save.disabled).toBe(true); // nothing changed
  await user.clear(box);
  await fill(user, box, "versao: 1\nexpiracao: um-dia");
  await user.click(screen.getByRole("button", { name: "Salvar política" }));
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("Linha 2:");
  expect(alert.textContent).toContain("expiracao: um-dia");
  expect(alert.textContent).toContain("Nada foi salvo");
  expect(box.value).toBe("versao: 1\nexpiracao: um-dia"); // what was typed is still there
  expect(calls.find((c) => c.method === "PUT")?.body).toEqual({
    text: "versao: 1\nexpiracao: um-dia",
    version: "v1",
  });
});

test("the extra instructions count their bytes and refuse to save over the limit", async () => {
  fakeServer({
    ...OWNER,
    "GET /api/rules/workspaces": () => ({ body: { workspaces: ["loja"] } }),
    "GET /api/workspaces/loja/instructions": () => ({
      body: { workspace: "loja", grand_master: "prefira Codex", table_masters: "", max_bytes: 20 },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/instrucoes");
  const gm = (await screen.findByLabelText("Instruções para o Grão-Mestre")) as HTMLTextAreaElement;
  expect(screen.getByText("13 bytes, até 20 bytes")).toBeTruthy();
  await fill(user, gm, " em revisão de código");
  const save = screen.getByRole("button", { name: "Salvar instruções" }) as HTMLButtonElement;
  expect(save.disabled).toBe(true);
  expect(screen.getAllByRole("alert").length).toBeGreaterThan(0);
});

test("the screens speak English when the language is English", async () => {
  fakeServer({
    ...OWNER,
    "GET /api/decrees": () => ({
      body: { workspaces: [{ workspace: "loja", decrees: [decree()] }] },
    }),
  });
  renderApp("/decretos", "en");
  expect(await screen.findByRole("heading", { name: "Decrees", level: 1 })).toBeTruthy();
  expect(await screen.findByText("Permanent rule", { selector: "span" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Revoke" })).toBeTruthy();
});

// the status strip of the screen: the one status that opens with a tone symbol
const strips = () =>
  screen.queryAllByRole("status").filter((el) => el.querySelector("p > span[aria-hidden]"));

// A fresh install: every read answers with nothing, and the screens still say what they are for.
const EMPTY: Record<string, { path: string; routes: Record<string, unknown>; empty: string }> = {
  decrees: {
    path: "/decretos",
    routes: { "GET /api/decrees": { workspaces: [{ workspace: "loja", decrees: [] }] } },
    empty: "Para que serve um decreto?",
  },
  delegations: {
    path: "/delegacoes",
    routes: {
      "GET /api/delegations": {
        workspaces: [{ workspace: "loja", delegations: [], subdelegations: [], rules: [] }],
        can_revoke_all: true,
        can_revoke: true,
      },
    },
    empty: "Ninguém decide por você",
  },
  chain: {
    path: "/cadeia",
    routes: { "GET /api/chain": { workspaces: [], can_pause: true } },
    empty: "A fila aparece com a primeira mesa",
  },
  ceiling: {
    path: "/teto",
    routes: {
      "GET /api/rules/workspaces": { workspaces: ["loja"] },
      "GET /api/workspaces/loja/ceiling": {
        workspace: "loja",
        present: false,
        valid: false,
        text: "",
        widenings: [],
        can_widen: true,
      },
    },
    empty: "Sem arquivo de teto",
  },
};

// CA-ADM-17
test.each(Object.entries(EMPTY))(
  "%s on a fresh install has one title, a status strip and an empty state with a way out",
  async (_name, c) => {
    fakeServer({
      ...OWNER,
      ...Object.fromEntries(Object.entries(c.routes).map(([k, body]) => [k, () => ({ body })])),
    });
    renderApp(c.path);
    const empty = await screen.findByRole("heading", { name: c.empty });
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(screen.getByText("Vocabulário:")).toBeTruthy();
    expect(strips()).toHaveLength(1);
    // the way out is the main action of the strip or, with no campaign, the one of the empty state
    const box = empty.closest("section") as HTMLElement;
    const strip = strips()[0] as HTMLElement;
    expect(box.querySelector("button, a") ?? strip.querySelector("button, a")).toBeTruthy();
  },
);

// CA-ADM-18
test.each([
  ["/decretos", "GET /api/decrees"],
  ["/delegacoes", "GET /api/delegations"],
  ["/cadeia", "GET /api/chain"],
  ["/teto", "GET /api/rules/workspaces"],
])(
  "%s with a failed read shows what, why, a retry and the raw answer folded",
  async (path, route) => {
    fakeServer({
      ...OWNER,
      [route]: () => ({
        status: 500,
        body: { error: { code: "internal", message: "db locked: boom" } },
      }),
    });
    renderApp(path);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Não foi possível carregar");
    expect(strips()).toHaveLength(0); // no state is claimed when it is not known
    expect(screen.getByRole("button", { name: "Tentar de novo" })).toBeTruthy();
    const details = screen.getByText("Detalhes técnicos").closest("details") as HTMLDetailsElement;
    expect(details.open).toBe(false);
    expect(details.textContent).toContain("db locked: boom");
  },
);

// CA-ADM-23
test("the chain screen tells Pausar autonomia from Pausar mesa in its first lines", async () => {
  fakeServer({ ...OWNER, "GET /api/chain": () => ({ body: chainBody() }) });
  renderApp("/cadeia");
  const intro = await screen.findByText(/Pausar autonomia só tira do Mestre da mesa/);
  expect(intro.textContent).toContain("os pedidos sobem ao Grão-Mestre e o trabalho continua");
  expect(intro.textContent).toContain("Para parar a mesa de vez, use Pausar mesa, dentro dela");
  expect(screen.getByRole("heading", { name: "Supervisão da cadeia", level: 1 })).toBeTruthy();
  await waitFor(() => expect(strips()).toHaveLength(1));
  expect(strips()[0]?.textContent).toContain("4 pedidos na fila, 1 mesa(s) com autonomia pausada");
});

// CA-ADM-24
test("the queue is a sentence per level and the buttons are tall enough to touch", async () => {
  fakeServer({ ...OWNER, "GET /api/chain": () => ({ body: chainBody() }) });
  renderApp("/cadeia");
  const queue = (
    await screen.findAllByRole("list", { name: "Fila de aprovações" })
  )[0] as HTMLElement;
  expect(queue.textContent).toContain("Pessoas: 1 pedido, o mais antigo há");
  expect(screen.queryByRole("table")).toBeNull();
  const main = within(screen.getByRole("main"));
  for (const b of main.getAllByRole("button")) {
    if (!b.getAttribute("aria-label")?.startsWith("Ajuda sobre"))
      expect(b.className).toContain("min-h-11");
  }
});

// CA-ADM-25
test("an Administrator opening the policy by its address sees the level it needs and no action", async () => {
  fakeServer({
    ...caps("status"),
    "GET /api/me": () => ({
      body: { ...ME, user: { ...ME.user, level: "administrator" }, capabilities: ["status"] },
    }),
    "GET /api/rules/workspaces": () => ({ body: { workspaces: ["loja"] } }),
    "GET /api/workspaces/loja/policy": () => ({
      status: 403,
      body: {
        error: {
          code: "forbidden_level",
          message: "x",
          params: { need: "owner", have: "administrator" },
        },
      },
    }),
  });
  renderApp("/politica");
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain(
    "Esta ação exige o nível Senhor da Guilda; o seu é Administrador.",
  );
  expect(screen.queryByRole("button", { name: /Salvar/ })).toBeNull();
});

// CA-ADM-25
test("buttons a level cannot press show the level they need", async () => {
  fakeServer({
    ...caps("status", "revoke_temporary"),
    "GET /api/me": () => ({
      body: {
        ...ME,
        user: { ...ME.user, level: "administrator" },
        capabilities: ["status", "revoke_temporary"],
      },
    }),
    "GET /api/delegations": () => ({ body: delegations }),
  });
  renderApp("/delegacoes");
  const renew = await screen.findByRole("button", { name: "Renovar" });
  expect((renew as HTMLButtonElement).disabled).toBe(true);
  expect(
    screen.getAllByText("Exige o nível Senhor da Guilda; o seu é Administrador.").length,
  ).toBeGreaterThan(0);
});

// CA-ADM-07
test("Validar checks the text without saving and says the line when it does not read", async () => {
  const { calls } = fakeServer({
    ...OWNER,
    "GET /api/rules/workspaces": () => ({ body: { workspaces: ["loja"] } }),
    "GET /api/workspaces/loja/policy": () => ({
      body: { workspace: "loja", text: "versao: 1\n", version: "v1", default: false },
    }),
    "POST /api/workspaces/loja/policy/validate": (c) =>
      (c.body as { text: string }).text.includes("ruim")
        ? {
            status: 422,
            body: {
              error: { code: "invalid_policy", message: "x", params: { line: 2, reason: "ruim" } },
            },
          }
        : { status: 204 },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/politica");
  const box = (await screen.findByLabelText("politica-de-aprovacao.yaml")) as HTMLTextAreaElement;
  const validate = screen.getByRole("button", { name: "Validar" }) as HTMLButtonElement;
  expect(validate.disabled).toBe(true);
  await user.clear(box);
  await fill(user, box, "versao: 1\nexpiracao: 24h");
  await user.click(screen.getByRole("button", { name: "Validar" }));
  expect(await screen.findByText("Política válida: pode salvar.")).toBeTruthy();
  await user.clear(box);
  await fill(user, box, "versao: 1\nruim: 1");
  await user.click(screen.getByRole("button", { name: "Validar" }));
  expect((await screen.findByRole("alert")).textContent).toContain("Linha 2:");
  expect(calls.some((c) => c.method === "PUT")).toBe(false); // validating never saves
});

test("one Salvar instruções saves both texts, each one with its own role", async () => {
  const { calls } = fakeServer({
    ...OWNER,
    "GET /api/rules/workspaces": () => ({ body: { workspaces: ["loja"] } }),
    "GET /api/workspaces/loja/instructions": () => ({
      body: { workspace: "loja", grand_master: "a", table_masters: "b", max_bytes: 4096 },
    }),
    "PUT /api/workspaces/loja/instructions": (c) => ({
      body: {
        workspace: "loja",
        grand_master: (c.body as { role: string }).role === "grand_master" ? "novo gm" : "a",
        table_masters: (c.body as { role: string }).role === "table_masters" ? "novo mm" : "b",
        max_bytes: 4096,
      },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/instrucoes");
  await fill(user, await screen.findByLabelText("Instruções para o Grão-Mestre"), "novo gm");
  await fill(user, screen.getByLabelText("Instruções para os Mestres de mesa"), "novo mm");
  await user.click(screen.getByRole("button", { name: "Salvar instruções" }));
  expect(await screen.findByText("Instruções salvas.")).toBeTruthy();
  const puts = calls
    .filter((c) => c.method === "PUT")
    .map((c) => (c.body as { role: string }).role);
  expect(puts).toEqual(["grand_master", "table_masters"]);
});
