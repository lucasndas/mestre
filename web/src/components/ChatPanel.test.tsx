import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { ChatConversation, ChatEntry, ChatOverview } from "../gen/panel";
import { emit, installFakeSocket, openSocket } from "../test-socket";
import { fakeServer, fill, ME, renderApp } from "../test-utils";
import { table, view } from "../workspace/test-fixtures";

beforeEach(() => installFakeSocket());
afterEach(() => vi.unstubAllGlobals());

const ANA = {
  ...ME,
  user: { ...ME.user, level: "approver", level_name: "Aprovador", workspaces: ["loja"] },
  capabilities: ["talk_to_grand_master", "status"],
};

const MB = 1 << 20;

const conv = (extra: Partial<ChatConversation> = {}): ChatConversation => ({
  workspace: "loja",
  can_talk: true,
  status: { state: "idle", ahead: 0, locked: false },
  entries: [],
  unread: 0,
  limit: 20 * MB,
  max_files: 5,
  ...extra,
});

const entry = (e: Partial<ChatEntry>): ChatEntry => ({
  id: "in-1",
  from: "user",
  channel: "panel",
  text: "oi",
  at: "2026-10-26T15:00:00Z",
  ...e,
});

const overview = (unread = 0): ChatOverview => ({
  workspaces: [{ slug: "loja", can_talk: true, unread }],
  unread,
  limit: 20 * MB,
  max_files: 5,
});

const base = (
  c: ChatConversation = conv(),
  unread = 0,
  extra: Record<string, () => unknown> = {},
) => ({
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/me": () => ({ body: ANA }),
  "GET /api/notices": () => ({ body: { notices: [], unread: 0 } }),
  "GET /api/chat": () => ({ body: overview(unread) }),
  "GET /api/chat/loja": () => ({ body: c }),
  ...extra,
});

async function openPanel(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    await screen.findByRole("button", { name: /Conversa com o Grão-Mestre|Abrir a conversa/ }),
  );
  return screen.findByRole("complementary", { name: "Conversa com o Grão-Mestre" });
}

function online() {
  let s: ReturnType<typeof openSocket> | undefined;
  act(() => {
    s = openSocket();
  });
  if (!s) throw new Error("no socket");
  return s;
}

// The side panel of the workspace: the state of the Grão-Mestre at the top, the history with the
// channel icon of each message (the Telegram's too), and the messages of the Grão-Mestre as
// Markdown with no active HTML.
test("the panel shows the state, the history of both channels and the Markdown of the Grão-Mestre with no active HTML", async () => {
  fakeServer(
    base(
      conv({
        status: { state: "busy", ahead: 2, locked: false },
        entries: [
          entry({ id: "in-1", channel: "telegram", text: "pelo telegram" }),
          entry({
            id: "out-2",
            from: "grand_master",
            channel: "telegram",
            text: "**pronto** veja [a doc](https://example.com/doc) e [mau](javascript:alert(1)) <img src=x onerror=alert(1)> <script>alert(2)</script>\n\n# Título\n\n![pixel](https://tracker.example/p.png)",
          }),
          entry({ id: "in-3", channel: "panel", text: "pelo painel" }),
        ],
      }),
    ),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  expect(await within(panel).findByText("Trabalhando no seu pedido")).toBeTruthy();
  expect(within(panel).getByText("Na fila: 2")).toBeTruthy();
  // the icons of the channel are the text a screen reader gets
  expect(within(panel).getAllByRole("img", { name: "pelo Telegram" })).toHaveLength(2);
  expect(within(panel).getAllByRole("img", { name: "pelo painel" })).toHaveLength(1);
  const gm = panel.querySelector('[data-entry="out-2"]') as HTMLElement;
  expect(gm.querySelector("strong")?.textContent).toBe("pronto");
  const link = within(gm).getByRole("link", { name: "a doc" });
  expect(link.getAttribute("href")).toBe("https://example.com/doc");
  expect(link.getAttribute("target")).toBe("_blank");
  expect(link.getAttribute("rel")).toContain("noopener");
  // nothing active: no tag of the agent became a tag, the unsafe link has no href, no image loads
  expect(gm.querySelector("img, script, [onerror]")).toBeNull();
  expect(within(gm).getByText("mau").closest("a")).toBeNull();
  expect(gm.querySelector("h1, h2, h3")).toBeNull();
  expect(gm.innerHTML).not.toContain("tracker.example");
  expect(gm.textContent).not.toMatch(/<\/?(script|img)/);
});

// The input is off, with the reason, for the level that may not talk to the Grão-Mestre.
test("a level that cannot talk sees the input off with the reason", async () => {
  fakeServer(base(conv({ can_talk: false })));
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  const field = await within(panel).findByLabelText("Mensagem para o Grão-Mestre");
  expect((field as HTMLTextAreaElement).disabled).toBe(true);
  expect(
    within(panel).getByText(/O seu nível \(Aprovador\) não permite falar com o Grão-Mestre/),
  ).toBeTruthy();
  expect(
    (within(panel).getByRole("button", { name: "Enviar" }) as HTMLButtonElement).disabled,
  ).toBe(true);
});

// A message goes with its id; sending it clears the field and the answer shows after.
test("a message is sent with its files, the field is cleared and the conversation is read again", async () => {
  let c = conv();
  const { calls } = fakeServer({
    ...base(c),
    "GET /api/chat/loja": () => ({ body: c }),
    "POST /api/chat/loja/messages": () => {
      c = conv({ entries: [entry({ id: "in-9", text: "abra a mesa" })] });
      return { status: 201, body: { id: "in-9", held: false } };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  await fill(
    user,
    await within(panel).findByLabelText("Mensagem para o Grão-Mestre"),
    "abra a mesa",
  );
  const pdf = new File(["%PDF-1.4"], "plano.pdf", { type: "application/pdf" });
  await user.upload(within(panel).getByLabelText("Anexar"), pdf);
  expect(within(panel).getByText(/plano\.pdf · 8 B/)).toBeTruthy();
  await user.click(within(panel).getByRole("button", { name: "Enviar" }));
  expect(await within(panel).findByText("Mensagem enviada.")).toBeTruthy();
  const post = calls.find((x) => x.method === "POST");
  expect(post?.path).toBe("/api/chat/loja/messages");
  const body = post?.body as Record<string, unknown>;
  expect(body.text).toBe("abra a mesa");
  expect(body.files).toEqual(["plano.pdf"]);
  expect(typeof body.client_id).toBe("string");
  expect(body.language).toBe("pt-BR"); // the language the panel is open in goes with the message
  expect(
    (within(panel).getByLabelText("Mensagem para o Grão-Mestre") as HTMLTextAreaElement).value,
  ).toBe("");
  expect(await within(panel).findByText("abra a mesa")).toBeTruthy();
});

// A file over the limit is refused before the send, with the limit shown.
test("a file over the limit is refused before sending, with the limit", async () => {
  const { calls } = fakeServer(base(conv({ limit: 10 })));
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  await within(panel).findByLabelText("Mensagem para o Grão-Mestre");
  // the limit is said when it is passed, not before
  expect(within(panel).queryByText(/Cada arquivo, até/)).toBeNull();
  await user.upload(
    within(panel).getByLabelText("Anexar"),
    new File(["x".repeat(11)], "grande.bin"),
  );
  expect((await within(panel).findByRole("alert")).textContent).toBe(
    "grande.bin passa do limite de 10 B. Escolha um arquivo menor.",
  );
  expect(within(panel).queryByText(/grande\.bin ·/)).toBeNull();
  expect(calls.some((x) => x.method === "POST")).toBe(false);
});

// With the WebSocket down, what is typed stays in the field and nothing is sent blind.
test("with the channel down the typed message stays in the field and is not sent", async () => {
  const { calls } = fakeServer(base());
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  const send = () => within(panel).getByRole("button", { name: "Enviar" }) as HTMLButtonElement;
  const field = await within(panel).findByLabelText("Mensagem para o Grão-Mestre");
  await fill(user, field, "não perca isto");
  // the socket did not open yet: no sending
  expect(send().disabled).toBe(true);
  expect(
    within(panel).getByText(/Sem conexão com a Cidadela\. O que você escreveu fica aqui/),
  ).toBeTruthy();
  const sock = openSocket();
  await waitFor(() => expect(send().disabled).toBe(false));
  // and the channel falls again
  act(() => sock.onclose?.({ code: 1006 }));
  await waitFor(() => expect(send().disabled).toBe(true));
  expect((field as HTMLTextAreaElement).value).toBe("não perca isto");
  expect(calls.some((x) => x.method === "POST")).toBe(false);
});

// A failure of the request keeps the text too; the same id is used on the retry.
test("a refusal keeps what was typed and says why", async () => {
  fakeServer({
    ...base(),
    "POST /api/chat/loja/messages": () => ({
      status: 422,
      body: { error: { code: "chat_blocked", message: "x", params: { minutes: 10 } } },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  const field = await within(panel).findByLabelText("Mensagem para o Grão-Mestre");
  await fill(user, field, "meu token é segredo");
  await user.click(within(panel).getByRole("button", { name: "Enviar" }));
  expect((await within(panel).findByRole("alert")).textContent).toMatch(
    /bloqueado por tentativas de mandar segredos\. Tente de novo em 10 min/,
  );
  expect((field as HTMLTextAreaElement).value).toBe("meu token é segredo");
  // the sending is blocked: the field and Enviar are off, and the notice has its first sentence in bold
  expect((field as HTMLTextAreaElement).disabled).toBe(true);
  expect(within(panel).getByRole("button", { name: "Enviar" }).hasAttribute("disabled")).toBe(true);
  expect(within(panel).getByRole("alert").querySelector("strong")?.textContent).toMatch(
    /bloqueado/,
  );
});

// With the system locked the send says "guardado até destravar", on the screen and on the message.
test("with the system locked the send says it is kept until it is unlocked", async () => {
  let c = conv({ status: { state: "idle", ahead: 0, locked: true } });
  fakeServer({
    ...base(c),
    "GET /api/chat/loja": () => ({ body: c }),
    "POST /api/chat/loja/messages": () => {
      c = conv({
        status: { state: "idle", ahead: 1, locked: true },
        entries: [entry({ id: "in-4", text: "guarde", waiting: true })],
      });
      return { status: 201, body: { id: "in-4", held: true } };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  expect(
    await within(panel).findByText(
      /Sistema travado: o que você enviar fica guardado até destravar/,
    ),
  ).toBeTruthy();
  // the line of status says it in two words, with the red dot, as the prototype
  expect(within(panel).getByRole("status").textContent).toContain("Sistema travado");
  expect(within(panel).getByRole("status").textContent).toContain("envio guardado");
  await fill(user, within(panel).getByLabelText("Mensagem para o Grão-Mestre"), "guarde");
  await user.click(within(panel).getByRole("button", { name: "Enviar" }));
  expect(await within(panel).findByText("Mensagem guardada até destravar.")).toBeTruthy();
  expect(await within(panel).findByText("Guardada até destravar")).toBeTruthy();
});

// The engine keeps a notice in place of what the filter of secrets held or cut: the message of the
// user shows the band of what happened, not the notice of the engine.
test("a held message shows Não entregue and a message with the secret cut shows Enviada sem o segredo", async () => {
  const c = conv({
    entries: [
      entry({
        id: "in-1",
        text: "[mensagem retida: o usuário mandou uma senha pelo painel; o motor não a entregou.]",
      }),
      entry({ id: "in-2", text: "Use o token [segredo removido] no deploy" }),
    ],
  });
  fakeServer({ ...base(c), "GET /api/chat/loja": () => ({ body: c }) });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  expect(await within(panel).findByText("Não entregue.")).toBeTruthy();
  expect(within(panel).queryByText(/mensagem retida/)).toBeNull();
  expect(within(panel).getByRole("link", { name: "Segredos" })).toBeTruthy();
  expect(within(panel).getByText("Enviada sem o segredo.")).toBeTruthy();
  expect(within(panel).getByText(/Use o token \[segredo removido\] no deploy/)).toBeTruthy();
});

// CA-CONV-02 (the screen): the answer that came while the panel was closed is counted on the button,
// shown as new when opened, and read.
test("the unread answers are counted, shown as new and marked as read when the panel opens", async () => {
  let c = conv({
    unread: 2,
    entries: [
      entry({ id: "in-1", text: "e então?" }),
      entry({ id: "out-1", from: "grand_master", text: "a mesa abriu", unread: true }),
      entry({
        id: "out-2",
        from: "grand_master",
        text: "e já tem testes",
        unread: true,
        at: "2026-10-26T15:05:00Z",
      }),
    ],
  });
  let unread = 2;
  const { calls } = fakeServer({
    ...base(c, 2),
    "GET /api/chat": () => ({ body: overview(unread) }),
    "GET /api/chat/loja": () => ({ body: c }),
    "POST /api/chat/loja/read": () => {
      unread = 0;
      c = conv({ entries: c.entries.map((e) => ({ ...e, unread: false })) });
      return { status: 204 };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const toggle = await screen.findByRole("button", { name: "Conversa com o Grão-Mestre, 2 novas" });
  await user.click(toggle);
  const panel = await screen.findByRole("complementary", { name: "Conversa com o Grão-Mestre" });
  expect(await within(panel).findByText("a mesa abriu")).toBeTruthy();
  await waitFor(() => expect(calls.some((x) => x.path === "/api/chat/loja/read")).toBe(true));
  // read: the counter goes, and the mark of "new" stays while the panel is open
  await screen.findByRole("button", { name: "Abrir a conversa com o Grão-Mestre" });
  expect(within(panel).getByText("Novas")).toBeTruthy();
});

// CA-CONV-09 (the screen): a button per option, only in the channel of the question; the choice goes
// once, and a second click is refused with the reason.
test("the question shows a button per option and the choice goes once", async () => {
  let c = conv({
    entries: [
      entry({
        id: "ask-5",
        from: "question",
        text: "Qual banco?",
        table: "42-login",
        question: { id: 5, options: ["postgres", "sqlite", "mysql"], state: "open" },
      }),
      entry({
        id: "ask-6",
        from: "question",
        channel: "telegram",
        text: "Pelo telegram?",
        question: { id: 6, options: ["a", "b"], state: "open" },
      }),
    ],
  });
  let taken = false;
  const { calls } = fakeServer({
    ...base(c),
    "GET /api/chat/loja": () => ({ body: c }),
    "POST /api/chat-choices/5": () => {
      if (taken)
        return { status: 409, body: { error: { code: "chat_choice_answered", message: "x" } } };
      taken = true;
      c = conv({
        entries: [
          {
            ...c.entries[0],
            question: {
              id: 5,
              options: ["postgres", "sqlite", "mysql"],
              state: "answered",
              answer: "sqlite",
            },
          } as ChatEntry,
          c.entries[1] as ChatEntry,
        ],
      });
      return { status: 204 };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  const group = await within(panel).findByRole("group", { name: "Opções da pergunta" });
  // the card of a question has the border of the accent, the question in bold and the options in a row
  expect(group.closest("li")?.className).toContain("border-accent");
  expect(group.className).toContain("flex-wrap");
  expect(
    within(group)
      .getAllByRole("button")
      .map((b) => b.textContent),
  ).toEqual(["postgres", "sqlite", "mysql"]);
  // the question of the Telegram has no buttons here
  expect(within(panel).getByText("Pergunta feita pelo Telegram: responda por lá.")).toBeTruthy();
  await user.click(within(group).getByRole("button", { name: "sqlite" }));
  await waitFor(() => expect(within(panel).getByText("Você escolheu: sqlite")).toBeTruthy());
  expect(calls.filter((x) => x.method === "POST" && x.path === "/api/chat-choices/5")).toHaveLength(
    1,
  );
  expect(calls.find((x) => x.method === "POST")?.body).toEqual({ option: 1, language: "pt-BR" });
  expect(within(panel).queryByRole("group", { name: "Opções da pergunta" })).toBeNull();
});

test("a second click on the same question is refused with the reason", async () => {
  const c = conv({
    entries: [
      entry({
        id: "ask-5",
        from: "question",
        text: "Qual?",
        question: { id: 5, options: ["a", "b"], state: "open" },
      }),
    ],
  });
  fakeServer({
    ...base(c),
    "POST /api/chat-choices/5": () => ({
      status: 409,
      body: { error: { code: "chat_choice_answered", message: "x" } },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  await user.click(await within(panel).findByRole("button", { name: "a" }));
  expect((await within(panel).findByRole("alert")).textContent).toBe(
    "Essa pergunta já foi respondida.",
  );
});

// CA-CONV-09 (the card): the message of progress is a card at the top, rewritten in place when the
// engine makes a new one: never two cards.
test("the progress is one card at the top, updated in place", async () => {
  let c = conv({
    progress: {
      lines: ["2 mesas, 1 trabalhando", "- Mesa 42: aguardando você"],
      updated_at: "2026-10-26T15:00:00Z",
    },
  });
  fakeServer({ ...base(c), "GET /api/chat/loja": () => ({ body: c }) });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  const sock = online();
  const card = await within(panel).findByRole("group", { name: "Andamento da campanha" });
  expect(card.textContent).toContain("2 mesas, 1 trabalhando");
  expect(within(card).getByText("Mesa 42: aguardando você")).toBeTruthy();
  c = conv({ progress: { lines: ["2 mesas, 2 trabalhando"], updated_at: "2026-10-26T17:00:00Z" } });
  act(() => emit(sock, "chat:u1", "changed", { workspace: "loja" }));
  await waitFor(() => expect(within(panel).getByText("2 mesas, 2 trabalhando")).toBeTruthy());
  expect(within(panel).queryByText("2 mesas, 1 trabalhando")).toBeNull();
  expect(within(panel).getAllByRole("group", { name: "Andamento da campanha" })).toHaveLength(1);
});

// The state of the Grão-Mestre follows the events, with no new read.
test("the state at the top follows the events of the channel", async () => {
  const { calls } = fakeServer(base());
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  const sock = online();
  expect(await within(panel).findByText("Pronto para ouvir")).toBeTruthy();
  const reads = calls.filter((x) => x.path === "/api/chat/loja").length;
  act(() =>
    emit(sock, "chat:u1", "status", {
      workspace: "loja",
      status: { state: "unavailable", reason: "o provedor caiu", ahead: 3, locked: false },
    }),
  );
  expect(await within(panel).findByText("Grão-Mestre indisponível: o provedor caiu")).toBeTruthy();
  expect(within(panel).getByText("Na fila: 3")).toBeTruthy();
  expect(calls.filter((x) => x.path === "/api/chat/loja").length).toBe(reads);
});

// A message can quote another: the quote goes with the id, never with a table the browser made up.
test("replying to a message sends its id as the quote", async () => {
  const c = conv({
    entries: [
      entry({
        id: "ask-5",
        from: "question",
        text: "Qual banco?",
        table: "42-login",
        question: { id: 5, options: ["a", "b"], state: "answered", answer: "a" },
      }),
    ],
  });
  const { calls } = fakeServer({
    ...base(c),
    "POST /api/chat/loja/messages": () => ({ status: 201, body: { id: "in-2", held: false } }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  await user.click(
    await within(panel).findByRole("button", {
      name: /Responder a esta mensagem de Pergunta do Grão-Mestre/,
    }),
  );
  expect(within(panel).getByText(/Respondendo a Grão-Mestre: Qual banco\?/)).toBeTruthy();
  await fill(user, within(panel).getByLabelText("Mensagem para o Grão-Mestre"), "sobre essa mesa");
  await user.click(within(panel).getByRole("button", { name: "Enviar" }));
  await waitFor(() => expect(calls.some((x) => x.method === "POST")).toBe(true));
  expect(calls.find((x) => x.method === "POST")?.body).toMatchObject({
    text: "sobre essa mesa",
    quote: "ask-5",
  });
  await waitFor(() => expect(within(panel).queryByText(/Respondendo a/)).toBeNull());
});

// The files of the Grão-Mestre are downloads; the others show by name.
test("the files of the Grão-Mestre are downloads", async () => {
  fakeServer(
    base(
      conv({
        entries: [
          entry({
            id: "out-3",
            from: "grand_master",
            text: "veja",
            attachments: [
              { name: "plano.pdf", download: "/api/chat/loja/files/3/0" },
              { name: "outro.pdf" },
            ],
          }),
          entry({ id: "in-4", text: "segue", attachments: [{ name: "contrato.pdf" }] }),
        ],
      }),
    ),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  const link = await within(panel).findByRole("link", { name: "Baixar plano.pdf" });
  expect(link.getAttribute("href")).toBe("/api/chat/loja/files/3/0");
  expect(link.hasAttribute("download")).toBe(true);
  expect(
    within(panel).getByText("outro.pdf (o arquivo ainda não está disponível para baixar)"),
  ).toBeTruthy();
  expect(within(panel).getByText("Arquivo contrato.pdf")).toBeTruthy();
});

test("the panel speaks English with the same screens", async () => {
  fakeServer(base(conv({ status: { state: "idle", ahead: 0, locked: false } })));
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/", "en");
  await user.click(
    await screen.findByRole("button", { name: "Open the chat with the Grand Master" }),
  );
  const panel = await screen.findByRole("complementary", { name: "Chat with the Grand Master" });
  expect(await within(panel).findByText("Ready to listen")).toBeTruthy();
  expect(within(panel).getByRole("button", { name: "Send" })).toBeTruthy();
  expect(within(panel).getByText("Ask for what you want done")).toBeTruthy();
});

// The filter of secrets acted on the server: the message went (as a notice or without the piece) and
// the user is told to change the secret.
test("when the filter held the message the user is told to change the secret", async () => {
  fakeServer({
    ...base(),
    "POST /api/chat/loja/messages": () => ({
      status: 201,
      body: { id: "in-5", held: false, secret: "held" },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  await fill(
    user,
    await within(panel).findByLabelText("Mensagem para o Grão-Mestre"),
    "a senha é x",
  );
  await user.click(within(panel).getByRole("button", { name: "Enviar" }));
  expect((await within(panel).findByRole("alert")).textContent).toMatch(/troque essa senha/);
});

// The design of the conversation (docs/painel/conversa-e-aprovacoes.md#desenho-da-conversa-m6-unidade-4f).

// CA-CONV-14: the first message: what to ask, in words, with three requests that only fill the field.
test("an empty conversation says what to ask and three requests fill the field without sending", async () => {
  const { calls } = fakeServer(base());
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  expect(await within(panel).findByText("Peça o que você quer ver pronto")).toBeTruthy();
  const picks = within(panel).getAllByRole("button", {
    name: /Adicione login com Google|Corrija o erro|Me explique/,
  });
  expect(picks).toHaveLength(3);
  await user.click(picks[0] as HTMLElement);
  const field = within(panel).getByLabelText("Mensagem para o Grão-Mestre") as HTMLTextAreaElement;
  expect(field.value).toBe("Adicione login com Google ao app");
  expect(calls.some((c) => c.method === "POST")).toBe(false);
});

// CA-CONV-15: the Grão-Mestre is working: the state, the queue and the sentence that the answer is kept.
test("while the Grão-Mestre works the state, the queue and the kept-answer sentence show, and go with the answer", async () => {
  let c = conv({
    status: { state: "busy", ahead: 1, locked: false },
    entries: [entry({ id: "in-1", text: "abra a mesa" })],
  });
  fakeServer({ ...base(c), "GET /api/chat/loja": () => ({ body: c }) });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  const sock = online();
  expect(await within(panel).findByText("Trabalhando no seu pedido")).toBeTruthy();
  expect(within(panel).getByText("Na fila: 1")).toBeTruthy();
  expect(within(panel).getByText(/Pode fechar a conversa: a resposta fica guardada/)).toBeTruthy();
  c = conv({
    status: { state: "idle", ahead: 0, locked: false },
    entries: [
      entry({ id: "in-1", text: "abra a mesa" }),
      entry({ id: "out-2", from: "grand_master", text: "aberta" }),
    ],
  });
  act(() => emit(sock, "chat:u1", "changed", { workspace: "loja" }));
  await within(panel).findByText("aberta");
  expect(within(panel).queryByText(/Pode fechar a conversa/)).toBeNull();
});

// CA-CONV-16: the one progress card: closed in a line, open with the tables and their situation; a reader
// who cannot write gets his own conversation, empty, and the field off with his level.
test("the progress card is closed in one line and opens to the tables with their situation; a reader sees his own conversation", async () => {
  const admin = {
    ...ANA,
    user: { ...ANA.user, level: "administrator", level_name: "Administrador" },
    capabilities: ["talk_to_grand_master", "status", "decide_approvals"],
  };
  const progress = { lines: ["2 mesas abertas"], updated_at: "2026-10-26T15:00:00Z" };
  fakeServer({
    ...base(conv({ progress })),
    "GET /api/me": () => ({ body: admin }),
    "GET /api/workspaces/loja/view": () => ({
      body: view([
        table({ id: "42-login", epic_title: "Login com Google" }),
        table({
          id: "43-carrinho",
          epic_title: "Carrinho",
          waits: [{ kind: "approval", reason: "pr", since: "2026-10-26T14:00:00Z" } as never],
        }),
      ]),
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  const card = await within(panel).findByRole("group", { name: "Andamento da campanha" });
  expect((card as HTMLDetailsElement).open).toBe(false);
  await waitFor(() => expect(card.textContent).toContain("2 mesas"));
  await user.click(card.querySelector("summary") as HTMLElement);
  expect((card as HTMLDetailsElement).open).toBe(true);
  expect(within(card).getByRole("link", { name: "Login com Google" })).toBeTruthy();
  expect(within(card).getByRole("link", { name: "Carrinho" })).toBeTruthy();
  expect(card.textContent).toContain("Trabalhando");
  expect(card.textContent).toContain("Espera a sua decisão");
  expect(within(card).getByText(/Precisa de você/)).toBeTruthy();
  expect(within(card).getByRole("link", { name: "Ver aprovações" })).toBeTruthy();
});

test("a reader who cannot write sees his own empty conversation, said not to be anyone else's, with the field off", async () => {
  fakeServer({
    ...base(conv({ can_talk: false })),
    "GET /api/me": () => ({
      body: { ...ANA, user: { ...ANA.user, level: "observer", level_name: "Observador" } },
    }),
    "GET /api/chat": () => ({
      body: { ...overview(), workspaces: [{ slug: "loja", can_talk: false, unread: 0 }] },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  expect(await within(panel).findByText("Você ainda não escreveu aqui")).toBeTruthy();
  expect(panel.textContent).toContain("as conversas dos outros usuários não aparecem aqui");
  expect(panel.textContent).toContain("(a sua, não a dos outros)");
  expect(within(panel).queryByRole("group", { name: "Andamento da campanha" })).toBeNull();
  expect(
    (within(panel).getByLabelText("Mensagem para o Grão-Mestre") as HTMLTextAreaElement).disabled,
  ).toBe(true);
  expect(panel.textContent).toContain(
    "O seu nível (Observador) não permite falar com o Grão-Mestre. Você vê a conversa, mas não escreve.",
  );
});

// CA-CONV-17: the filter of secrets: each answer says what happened and what to do; the field stays usable.
test("a held and a stripped message each say what happened and what to do, and the field stays usable", async () => {
  let secret: "held" | "removed" = "held";
  fakeServer({
    ...base(),
    "POST /api/chat/loja/messages": () => ({
      status: 201,
      body: { id: "in-5", held: false, secret },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const panel = await openPanel(user);
  online();
  const field = (await within(panel).findByLabelText(
    "Mensagem para o Grão-Mestre",
  )) as HTMLTextAreaElement;
  await fill(user, field, "minha frase de senha");
  await user.click(within(panel).getByRole("button", { name: "Enviar" }));
  expect((await within(panel).findByRole("alert")).textContent).toMatch(
    /não foi entregue.*troque essa senha/,
  );
  expect(field.disabled).toBe(false);
  expect(field.value).toBe("");
  secret = "removed";
  await fill(user, field, "o token é ghp_x");
  await user.click(within(panel).getByRole("button", { name: "Enviar" }));
  await waitFor(() =>
    expect(within(panel).getByRole("alert").textContent).toMatch(
      /sem o trecho.*Troque esse segredo/,
    ),
  );
});

// CA-CONV-18: a long history: day separators, the "Novas" line and the window of the last 200; the field and
// the buttons are the size of a finger.
test("a long history has day separators, the Novas line and the note of the last 200; the buttons are 44 px", async () => {
  const entries: ChatEntry[] = [];
  for (let i = 0; i < 200; i++)
    entries.push(
      entry({
        id: i % 2 ? `out-${i}` : `in-${i}`,
        from: i % 2 ? "grand_master" : "user",
        text: `mensagem ${i}`,
        at: `2026-10-${i < 100 ? "25" : "26"}T15:00:00Z`,
        unread: i >= 198,
      }),
    );
  fakeServer(base(conv({ entries, unread: 2 }), 2));
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const toggle = await screen.findByRole("button", { name: /Conversa com o Grão-Mestre, 2 novas/ });
  await user.click(toggle);
  const panel = await screen.findByRole("complementary", { name: "Conversa com o Grão-Mestre" });
  expect(await within(panel).findByText("Mostrando as últimas 200 mensagens.")).toBeTruthy();
  expect(await within(panel).findByText("Novas")).toBeTruthy();
  expect(within(panel).getAllByText(/domingo|segunda/).length).toBeGreaterThanOrEqual(2);
  expect(within(panel).getByRole("button", { name: "Enviar" }).className).toContain("min-h-11");
  expect(within(panel).getByLabelText("Mensagem para o Grão-Mestre").className).toContain(
    "min-h-20",
  );
});
