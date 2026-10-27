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
  await waitFor(() =>
    expect(screen.getByText("Nenhum decreto em vigor nesta campanha.")).toBeTruthy(),
  );
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
  await user.click(screen.getByRole("button", { name: "Ampliar o teto" }));
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
  const gm = (await screen.findByLabelText(
    "Texto: Instruções para o Grão-Mestre",
  )) as HTMLTextAreaElement;
  expect(screen.getByText("13 de 20 bytes")).toBeTruthy();
  await fill(user, gm, " em revisão de código");
  const form = screen.getByRole("form", { name: "Instruções para o Grão-Mestre" });
  const save = form.querySelector('button[type="submit"]') as HTMLButtonElement;
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
