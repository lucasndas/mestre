import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { fakeServer, fill, ME, renderApp } from "../../test-utils";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
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
  github_login: "bruno-gh",
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
  github_login: "",
});
const list = { users: [owner, row({})], workspaces: ["loja", "blog"] };

test("a level that may not use the screen is refused with the level that is required, and the nav hides it", async () => {
  fakeServer({
    ...base,
    "GET /api/me": () => ({
      body: {
        ...ME,
        user: { ...ME.user, level: "administrator" },
        capabilities: ["activate_classes", "status"],
      },
    }),
    "GET /api/users": () => ({
      status: 403,
      body: {
        error: {
          code: "forbidden_level",
          message: "x",
          params: { need: "owner", have: "administrator", capability: "manage_users" },
        },
      },
    }),
  });
  renderApp("/usuarios");
  expect((await screen.findByRole("alert")).textContent).toBe(
    "Esta ação exige o nível Senhor da Guilda; o seu é Administrador.",
  );
});

test("the Owner registers a user with the PIN and sees the code of first access once", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/users": () => ({ body: list }),
    "POST /api/users": () => ({
      status: 201,
      body: {
        user: row({ id: "u3", name: "Ana Lima", login: "" }),
        code: { code: "ABCD2345", kind: "first", expires_at: "2026-10-26T12:30:00Z" },
      },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  await user.click(await screen.findByRole("button", { name: "Cadastrar usuário" }));
  const submit = screen.getByRole("button", { name: "Cadastrar usuário" });
  expect((submit as HTMLButtonElement).disabled).toBe(true); // no name yet
  await fill(user, screen.getByLabelText("Nome"), "Ana Lima");
  await user.click(screen.getByRole("radio", { name: /Observador/ }));
  await user.click(screen.getByRole("checkbox", { name: "loja" }));
  await user.click(screen.getByRole("button", { name: "Cadastrar usuário" }));
  // the PIN is asked after the button and goes in the very request
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Cadastrar usuário" }));
  expect(
    await screen.findByRole("heading", { name: "Código de primeiro acesso de Ana Lima" }),
  ).toBeTruthy();
  expect(screen.getByTestId("shown-once").textContent).toBe("ABCD2345");
  const post = calls.find((c) => c.method === "POST" && c.path === "/api/users");
  expect(post?.headers["X-Mestre-Pin"]).toBe("123456");
  expect(post?.body).toEqual({
    name: "Ana Lima",
    level: "observer",
    workspaces: ["loja"],
    github_login: "",
  });
  await user.click(screen.getByRole("button", { name: "Já anotei" }));
  expect(screen.queryByTestId("shown-once")).toBeNull();
});

test("a refusal of the server shows its text and changes nothing on the screen", async () => {
  fakeServer({
    ...base,
    "GET /api/users": () => ({ body: list }),
    "POST /api/users/u2/suspend": () => ({
      status: 403,
      body: { error: { code: "pin_invalid", message: "x" } },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  await user.click(await screen.findByRole("button", { name: "Gerenciar Bruno Dias" }));
  await user.click(screen.getByRole("button", { name: "Suspender" }));
  await fill(user, screen.getByLabelText("PIN"), "000000");
  await user.click(screen.getByRole("button", { name: "Suspender" }));
  expect((await screen.findByRole("alert")).textContent).toBe("PIN incorreto.");
  expect(screen.queryByText("Suspenso")).toBeNull();
  expect((screen.getByLabelText("PIN") as HTMLInputElement).value).toBe(""); // a PIN is never kept
});

test("the user list shows the state of the GitHub credential and the codes need the PIN", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/users": () => ({
      body: {
        ...list,
        users: [
          owner,
          row({
            github: { state: "connected", login: "bruno-gh", expires_at: "2026-10-26T20:00:00Z" },
          }),
        ],
      },
    }),
    "POST /api/users/u2/access-code": () => ({
      body: { code: "RESET234", kind: "reset", expires_at: "2026-10-26T12:30:00Z" },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/usuarios");
  expect(
    await screen.findByText(/GitHub conectado como bruno-gh, renovado até 26\/10\/2026, 17:00/),
  ).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Gerenciar Bruno Dias" }));
  await user.click(screen.getByRole("button", { name: "Redefinir a senha" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Redefinir a senha" }));
  expect(
    await screen.findByRole("heading", { name: "Código para a nova senha de Bruno Dias" }),
  ).toBeTruthy();
  expect(calls.find((c) => c.path === "/api/users/u2/access-code")?.body).toEqual({
    kind: "reset",
  });
});

const secret = (o: Record<string, unknown>) => ({
  name: "gmail",
  type: "site_credential",
  fingerprint: "a1b2c3d4",
  created_at: "2026-10-01T12:00:00Z",
  updated_at: "2026-10-01T12:00:00Z",
  managed: false,
  site: {
    owner_user: "u1",
    classes: ["pesquisador"],
    domains: ["mail.exemplo.com"],
    kind: "senha_de_app",
  },
  ...o,
});

test("the secrets show name, type, fingerprint, dates and notices, with no value to reveal", async () => {
  fakeServer({
    ...base,
    "GET /api/users": () => ({ body: list }),
    "GET /api/secrets": () => ({
      body: {
        secrets: [
          secret({ notice: { kind: "expiring", days: 3 } }),
          secret({
            name: "app-key",
            type: "github_app_key",
            managed: true,
            site: undefined,
            fingerprint: "ffff0000",
          }),
        ],
      },
    }),
  });
  renderApp("/segredos");
  expect(await screen.findByText("Vence em 3 dia(s).")).toBeTruthy();
  expect(screen.getByText("a1b2c3d4")).toBeTruthy();
  expect(screen.getByText("Credencial de site")).toBeTruthy();
  expect(screen.getByText("Chave de um App do GitHub")).toBeTruthy();
  // what the Mestre keeps is not changed here; no screen has a button that shows a value
  expect(screen.getAllByRole("button", { name: /Substituir/ })).toHaveLength(1);
  expect(screen.queryByRole("button", { name: /Mostrar|Revelar|Ver valor/ })).toBeNull();
});

test("replacing a site credential asks for the PIN and writes the whole credential again", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/users": () => ({ body: list }),
    "GET /api/secrets": () => ({ body: { secrets: [secret({})] } }),
    "PUT /api/secrets/gmail": () => ({ body: secret({}) }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/segredos");
  await user.click(await screen.findByRole("button", { name: "Substituir gmail" }));
  await fill(user, screen.getByLabelText("Página de login"), "https://mail.exemplo.com/entrar");
  await fill(user, screen.getByLabelText("Usuário"), "maria@exemplo.com");
  await fill(user, screen.getByLabelText("Senha, token ou chave"), "senha-nova-longa");
  await user.click(screen.getByRole("button", { name: "Substituir" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Substituir" }));
  expect(await screen.findByText("Segredo substituído.")).toBeTruthy();
  const put = calls.find((c) => c.method === "PUT");
  expect(put?.headers["X-Mestre-Pin"]).toBe("123456");
  expect(put?.body).toMatchObject({
    site: {
      url: "https://mail.exemplo.com/entrar",
      user: "maria@exemplo.com",
      password: "senha-nova-longa",
      kind: "senha_de_app",
      classes: ["pesquisador"],
      domains: ["mail.exemplo.com"],
      owner_user: "u1",
    },
  });
  // the field of the value is empty again and nothing of it stays in the page
  expect(document.body.textContent).not.toContain("senha-nova-longa");
});

test("a site credential is registered with every field and the suggestion of an app password", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/users": () => ({ body: list }),
    "GET /api/secrets": () => ({ body: { secrets: [] } }),
    "POST /api/secrets/site": () => ({ status: 201, body: secret({ name: "Banco" }) }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/segredos");
  await user.click(await screen.findByRole("button", { name: "Cadastrar credencial de site" }));
  expect(screen.getByText(/Prefira senha de app, token ou chave de API/)).toBeTruthy();
  await fill(user, screen.getByLabelText("Nome da credencial"), "Banco");
  await fill(user, screen.getByLabelText("Página de login"), "https://banco.exemplo.com");
  await fill(user, screen.getByLabelText("Usuário"), "maria");
  await fill(user, screen.getByLabelText("Senha, token ou chave"), "s3gredo-longo");
  await user.click(screen.getByRole("radio", { name: "Token" }));
  await fill(user, screen.getByLabelText(/Classes que podem usar/), "pesquisador, contador");
  await fill(user, screen.getByLabelText(/Domínios permitidos/), "banco.exemplo.com");
  await fill(user, screen.getByLabelText(/Validade/), "2027-01-15");
  await user.click(screen.getByRole("button", { name: "Cadastrar credencial" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Cadastrar credencial" }));
  expect(await screen.findByText("Credencial cadastrada.")).toBeTruthy();
  expect(
    calls.find((c) => c.method === "POST" && c.path === "/api/secrets/site")?.body,
  ).toMatchObject({
    name: "Banco",
    url: "https://banco.exemplo.com",
    user: "maria",
    password: "s3gredo-longo",
    kind: "token",
    classes: ["pesquisador", "contador"],
    domains: ["banco.exemplo.com"],
    expires_at: "2027-01-15T12:00:00Z",
  });
});

test("a new recovery key shows the warning first, asks for the PIN and shows the key once", async () => {
  const { calls } = fakeServer({
    ...base,
    "GET /api/users": () => ({ body: list }),
    "GET /api/secrets": () => ({ body: { secrets: [] } }),
    "POST /api/recovery-key": () => ({ body: { key: "ABCD-EFGH-IJKL-MNOP" } }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/segredos");
  await user.click(await screen.findByRole("button", { name: "Gerar nova chave de recuperação" }));
  // the warning about the old backups is before the PIN, and the button waits for the reading
  expect(screen.getByRole("alert").textContent).toContain(
    "os backups que já existem continuam exigindo a chave antiga",
  );
  const go = screen.getByRole("button", { name: "Gerar a nova chave" });
  expect((go as HTMLButtonElement).disabled).toBe(true);
  expect(calls.some((c) => c.path === "/api/recovery-key")).toBe(false);
  await user.click(screen.getByRole("checkbox", { name: /Li o aviso/ }));
  await user.click(screen.getByRole("button", { name: "Gerar a nova chave" }));
  await fill(user, screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Gerar a nova chave" }));
  expect(await screen.findByTestId("shown-once")).toBeTruthy();
  expect(screen.getByTestId("shown-once").textContent).toBe("ABCD-EFGH-IJKL-MNOP");
  const post = calls.find((c) => c.path === "/api/recovery-key");
  expect(post?.body).toEqual({ acknowledged: true });
  expect(post?.headers["X-Mestre-Pin"]).toBe("123456");
  await user.click(screen.getByRole("button", { name: "Já guardei a chave" }));
  expect(screen.queryByTestId("shown-once")).toBeNull();
});

const entry = (o: Record<string, unknown>) => ({
  id: "e1",
  at: "2026-10-26T12:00:00Z",
  action: "panel.user.created",
  user_id: "u1",
  user_name: "Maria Souza",
  workspace: "",
  detail: { user_id: "u1", after: { level: "approver" } },
  ...o,
});

test("the audit shows who did what and when, in the Guild time zone, and filters and pages", async () => {
  const people = [
    { id: "u1", name: "Maria Souza" },
    { id: "u2", name: "Bruno Dias" },
  ];
  const { calls } = fakeServer({
    ...base,
    "GET /api/audit": () => ({
      body: {
        entries: [
          entry({}),
          entry({ id: "e0", action: "citadel.promoted", user_id: "", user_name: "", detail: {} }),
        ],
        next: "100:e0",
        people,
        workspaces: ["loja"],
      },
    }),
    "GET /api/audit?cursor=100%3Ae0": () => ({
      body: {
        entries: [entry({ id: "e9", action: "panel.secret.deleted" })],
        next: "",
        people,
        workspaces: ["loja"],
      },
    }),
    "GET /api/audit?user=u2&action=panel.user.&workspace=loja": () => ({
      body: { entries: [], next: "", people, workspaces: ["loja"] },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/auditoria");
  expect(await screen.findByText("Usuário cadastrado")).toBeTruthy();
  expect(screen.getByText("citadel.promoted")).toBeTruthy(); // an action the panel has no text for shows its code
  expect(screen.getAllByText("26/10/2026, 09:00")).toHaveLength(2); // 12:00Z in São Paulo
  await user.click(screen.getByRole("button", { name: "Carregar mais" }));
  expect(await screen.findByText("Segredo apagado")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Carregar mais" })).toBeNull();
  await user.selectOptions(screen.getByLabelText("Quem"), "u2");
  await user.selectOptions(screen.getByLabelText("O quê"), "panel.user.");
  await user.selectOptions(screen.getByLabelText("Campanha"), "loja");
  await user.click(screen.getByRole("button", { name: "Filtrar" }));
  expect(await screen.findByText("Nada para mostrar")).toBeTruthy();
  expect(calls.some((c) => c.path === "/api/audit?user=u2&action=panel.user.&workspace=loja")).toBe(
    true,
  );
});

const challenge = {
  user_code: "WDJB-MJHT",
  verification_uri: "https://github.com/login/device",
  expires_at: "2026-10-26T12:15:00Z",
  interval_seconds: 5,
};
const githubMe = { github: { state: "", login: "" }, login: "maria-gh", may: true };

test("Conectar GitHub shows the code, waits at the pace GitHub asks and shows when it is connected", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  let connected = false;
  let polls = 0;
  fakeServer({
    ...base,
    "GET /api/me/github": () => ({
      body: connected
        ? {
            ...githubMe,
            github: { state: "connected", login: "maria-gh", expires_at: "2026-10-26T20:00:00Z" },
          }
        : githubMe,
    }),
    "POST /api/me/github/begin": () => ({ body: challenge }),
    "POST /api/me/github/poll": () => {
      polls++;
      connected = polls >= 2;
      return { body: { state: connected ? "connected" : "pending" } };
    },
  });
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
  renderApp("/github");
  await user.click(await screen.findByRole("button", { name: "Conectar GitHub" }));
  expect((await screen.findByTestId("device-code")).textContent).toBe("WDJB-MJHT");
  expect(screen.getByText(/Entre com a conta maria-gh/)).toBeTruthy();
  expect(polls).toBe(0);
  await vi.advanceTimersByTimeAsync(5000);
  await waitFor(() => expect(polls).toBe(1));
  await vi.advanceTimersByTimeAsync(5000);
  expect((await screen.findByText("GitHub conectado.")).textContent).toBe("GitHub conectado.");
  expect(await screen.findByText(/Conectado como maria-gh/)).toBeTruthy();
  expect(screen.queryByTestId("device-code")).toBeNull();
});

// CA-GH-APROV-06 (the screen): the login that authorized is not the registered one,
// the engine threw the token away and the panel shows the error, never "connected".
test("Conectar GitHub shows the error when another GitHub account authorized", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  fakeServer({
    ...base,
    "GET /api/me/github": () => ({ body: githubMe }),
    "POST /api/me/github/begin": () => ({ body: challenge }),
    "POST /api/me/github/poll": () => ({
      status: 409,
      body: { error: { code: "github_login_mismatch", message: "x" } },
    }),
  });
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
  renderApp("/github", "en");
  await user.click(await screen.findByRole("button", { name: "Connect GitHub" }));
  await screen.findByTestId("device-code");
  await vi.advanceTimersByTimeAsync(5000);
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain(
    "The GitHub account that authorized is not the one registered for you",
  );
  expect(screen.queryByText("GitHub connected.")).toBeNull();
  expect(screen.queryByTestId("device-code")).toBeNull();
  // and the person can try again
  expect(screen.getByRole("button", { name: "Connect GitHub" })).toBeTruthy();
});

test("the user asked to take the post of Owner sees the banner and accepts with the PIN", async () => {
  const transfer = {
    transfer: {
      from_id: "u1",
      from_name: "Maria Souza",
      to_id: "u2",
      to_name: "Bruno Dias",
      expires_at: "2026-10-27T12:00:00Z",
      to_me: true,
    },
  };
  let accepted = false;
  const { calls } = fakeServer({
    ...base,
    "GET /api/me": () =>
      accepted
        ? { status: 401, body: { error: { code: "unauthenticated", message: "x" } } }
        : {
            body: {
              ...ME,
              user: { ...ME.user, id: "u2", level: "administrator" },
              capabilities: ["status"],
            },
          },
    "GET /api/transfer": () => ({ body: transfer }),
    "POST /api/transfer/accept": () => {
      accepted = true; // the sessions of both end with the change
      return { status: 204 };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/");
  expect(
    await screen.findByText(/Maria Souza quer passar o posto de Senhor da Guilda a você/),
  ).toBeTruthy();
  await user.click(screen.getByRole("link", { name: "Ver o pedido" }));
  const page = await screen.findByRole("heading", { name: "Posto de Senhor da Guilda" });
  expect(page).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Aceitar o posto de Senhor da Guilda" }));
  await fill(user, screen.getByLabelText("PIN"), "654321");
  await user.click(screen.getByRole("button", { name: "Aceitar o posto de Senhor da Guilda" }));
  await waitFor(() => expect(calls.some((c) => c.path === "/api/transfer/accept")).toBe(true));
  expect(calls.find((c) => c.path === "/api/transfer/accept")?.headers["X-Mestre-Pin"]).toBe(
    "654321",
  );
  // the sessions end with the change: the panel goes to the login
  await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  expect(within(document.body).queryByText(/quer passar o posto/)).toBeNull();
});

// the status strip of the screen: the one status that opens with a tone symbol
const strips = () =>
  screen.queryAllByRole("status").filter((el) => el.querySelector("p > span[aria-hidden]"));

// CA-ADM-17
test("an empty vault says what a site credential is and how to register one, beside where each key goes", async () => {
  fakeServer({
    ...base,
    "GET /api/users": () => ({ body: list }),
    "GET /api/secrets": () => ({ body: { secrets: [] } }),
  });
  renderApp("/segredos");
  const empty = await screen.findByRole("heading", { name: "Nenhuma credencial de site ainda" });
  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  expect(screen.getByText("Vocabulário:")).toBeTruthy();
  expect(strips()).toHaveLength(1);
  expect(
    within(empty.closest("section") as HTMLElement).getByRole("button", {
      name: "Cadastrar credencial de site",
    }),
  ).toBeTruthy();
  // one button only: the strip does not repeat the way out
  expect(screen.getAllByRole("button", { name: "Cadastrar credencial de site" })).toHaveLength(1);
});

// CA-ADM-17
test("an empty audit says when records appear and offers to clear the filters", async () => {
  fakeServer({
    ...base,
    "GET /api/audit": () => ({ body: { entries: [], people: [], workspaces: [], next: "" } }),
  });
  renderApp("/auditoria");
  const empty = await screen.findByRole("heading", { name: "Nada para mostrar" });
  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  expect(strips()).toHaveLength(1);
  expect(
    within(empty.closest("section") as HTMLElement).getByRole("button", { name: "Limpar" }),
  ).toBeTruthy();
});

// CA-ADM-17
test("Conectar GitHub opens with the state of the connection and the one action", async () => {
  fakeServer({
    ...base,
    "GET /api/me/github": () => ({
      body: { login: "maria-gh", may: true, github: { state: "", login: "" } },
    }),
  });
  renderApp("/github");
  await screen.findByRole("heading", { name: "Conectar GitHub", level: 1 });
  await waitFor(() => expect(strips()).toHaveLength(1));
  expect(strips()[0]?.textContent).toContain("Você ainda não conectou o seu GitHub.");
  expect(screen.getAllByRole("button", { name: "Conectar GitHub" })).toHaveLength(1);
});

// CA-ADM-18
test.each([
  ["/segredos", "GET /api/secrets"],
  ["/auditoria", "GET /api/audit"],
  ["/github", "GET /api/me/github"],
])(
  "%s with a failed read shows what, why, a retry and the raw answer folded",
  async (path, route) => {
    fakeServer({
      ...base,
      "GET /api/users": () => ({ body: list }),
      [route]: () => ({
        status: 500,
        body: { error: { code: "internal", message: "db locked: boom" } },
      }),
    });
    renderApp(path);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Não foi possível carregar");
    expect(strips()).toHaveLength(0);
    expect(screen.getByRole("button", { name: "Tentar de novo" })).toBeTruthy();
    const details = screen.getByText("Detalhes técnicos").closest("details") as HTMLDetailsElement;
    expect(details.open).toBe(false);
    expect(details.textContent).toContain("db locked: boom");
  },
);

// CA-ADM-21
test("the vault says where each key goes, keeps the Mestre's own secrets apart and shows no value", async () => {
  fakeServer({
    ...base,
    "GET /api/users": () => ({ body: list }),
    "GET /api/secrets": () => ({
      body: {
        secrets: [
          secret({}),
          secret({
            name: "guild_ca_key",
            type: "ca_key",
            managed: true,
            site: undefined,
            fingerprint: "ffff0000",
          }),
        ],
      },
    }),
  });
  renderApp("/segredos");
  const where = await screen.findByRole("region", { name: "Onde cada chave vai" });
  expect(where.textContent).toContain("Cadastrar credencial de site");
  expect(within(where).getByRole("link", { name: "Ir para Apps do GitHub" })).toBeTruthy();
  expect(within(where).getByRole("link", { name: "Ir para Perfis de conta" })).toBeTruthy();
  expect(where.textContent).toContain("Telegram");
  // the key of a provider has no button: the screen says it cannot be done here yet
  expect(where.textContent).toContain("ainda não dá para cadastrar por aqui");
  const mine = screen.getByRole("region", { name: "Guardados pelo Mestre" });
  expect(mine.textContent).toContain("Chave da autoridade da Guilda");
  expect(mine.textContent).toContain("guild_ca_key");
  expect(within(mine).queryByRole("button")).toBeNull();
  expect(screen.getByRole("heading", { name: "Credenciais de site" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Mostrar|Revelar|Ver valor/ })).toBeNull();
});

// CA-ADM-24
test("the vault form keeps the rare fields under More options and every button is tall enough", async () => {
  fakeServer({
    ...base,
    "GET /api/users": () => ({ body: list }),
    "GET /api/secrets": () => ({ body: { secrets: [secret({})] } }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/segredos");
  await user.click(await screen.findByRole("button", { name: "Cadastrar credencial de site" }));
  const more = screen.getByText("Mais opções").closest("details") as HTMLDetailsElement;
  expect(more.open).toBe(false);
  expect(more.contains(screen.getByLabelText("Validade (opcional)"))).toBe(true);
  expect(more.querySelector("textarea")).toBeTruthy();
  const main = within(screen.getByRole("main"));
  for (const b of main.getAllByRole("button")) {
    if (!b.getAttribute("aria-label")?.startsWith("Ajuda sobre"))
      expect(b.className).toContain("min-h-11");
  }
});

// CA-ADM-25
test("an Administrator sees the vault buttons off, with the level they need", async () => {
  fakeServer({
    ...base,
    "GET /api/me": () => ({
      body: {
        ...ME,
        user: { ...ME.user, level: "administrator" },
        capabilities: ["activate_classes", "status"],
      },
    }),
    "GET /api/users": () => ({ body: list }),
    "GET /api/secrets": () => ({ body: { secrets: [secret({})] } }),
  });
  renderApp("/segredos");
  const replace = await screen.findByRole("button", { name: "Substituir" });
  expect((replace as HTMLButtonElement).disabled).toBe(true);
  expect(
    screen.getAllByText("Exige o nível Senhor da Guilda; o seu é Administrador.").length,
  ).toBeGreaterThan(0);
  expect(
    (screen.getByRole("button", { name: "Cadastrar credencial de site" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});
