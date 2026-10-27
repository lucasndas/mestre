import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { I18nextProvider } from "react-i18next";
import { afterEach, expect, test, vi } from "vitest";
import { SocketContext } from "../app/socket";
import type { CanvasState } from "../gen/panel";
import { setupI18n } from "../i18n";
import { ApiError } from "../lib/api";
import { UiSocket } from "../lib/ws";
import { fakeServer, ME } from "../test-utils";
import { actions } from "./api";
import { tableState } from "./fixtures";
import { useGestures } from "./gestures";
import { addArrow, emptyDraft } from "./mission";
import { TableCanvas, useRefusalText } from "./TableCanvas";

afterEach(() => vi.unstubAllGlobals());

const refused = (reason: string, message: string, params: object = {}) => ({
  status: 409,
  body: { error: { code: "refused", message, params: { reason, ...params } } },
});

function wrap(
  language: "pt-BR" | "en" = "pt-BR",
  sock: UiSocket | null = null,
  me: typeof ME = ME,
) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(["me"], me);
  const i18n = setupI18n(language);
  // biome-ignore lint/correctness/noComponentHookFactories: a test helper, made once per test
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={qc}>
        <SocketContext.Provider value={sock}>{children}</SocketContext.Provider>
      </QueryClientProvider>
    </I18nextProvider>
  );
  return { qc, Wrapper };
}

const path = "/api/workspaces/loja/tables/m1/canvas";
const dev = { kind: "character", id: "dev" };
const rev = { kind: "character", id: "rev" };

/** The gestures with the real refusal text, over a fake server. */
function gestures(state: CanvasState) {
  const { Wrapper } = wrap();
  const notices: string[] = [];
  const changed = vi.fn();
  const view = renderHook(
    () => {
      const refusalText = useRefusalText();
      return useGestures({
        state,
        act: actions("loja", "m1"),
        changed,
        fail: (err) => notices.push(refusalText(err)),
        draft: null,
        setDraft: () => {},
        clear: () => notices.splice(0),
        onlyCharacters: () => {},
      });
    },
    { wrapper: Wrapper },
  );
  return { view, notices, changed };
}

test("the canvas draws the characters, the machine badge, the state, the mission step, the wait and the scroll", async () => {
  fakeServer({ [`GET ${path}`]: () => ({ body: tableState() }) });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  const devNode = await screen.findByRole("article", { name: "Personagem Dev-1" });
  expect(devNode.textContent).toContain("Máquina windows-pc");
  expect(devNode.textContent).toContain("Trabalhando");
  expect(screen.getByRole("article", { name: "Personagem Revisor" }).textContent).toContain(
    "Esperando Dev-1",
  );
  expect(screen.getByRole("article", { name: "Pergaminho Plano" })).toBeTruthy();
  // the Grand Master is pinned and is on the table, so that its automatic connection has two ends
  expect(screen.getByRole("button", { name: "Desafixar Grão-Mestre" })).toBeTruthy();
  // the lines need a real layout engine to be measured: the model test checks them and the browser smoke draws them
});

test("a deadlock shows in red with its cycle", async () => {
  fakeServer({
    [`GET ${path}`]: () => ({
      body: tableState({
        waits: [
          { id: "w1", waiter: "rev", target: "dev", kind: "ask", reason: "Dev-1", deadlock: true },
        ],
        deadlocks: [{ text: "Revisor → Dev-1 → Revisor" }],
      }),
    }),
  });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("Revisor → Dev-1 → Revisor");
});

// CA-CANVAS-06, the part of the screen: after a reload the elements are where the server keeps them.
test("reopening the page puts every node where the layout says", async () => {
  fakeServer({ [`GET ${path}`]: () => ({ body: tableState() }) });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  await screen.findByRole("article", { name: "Personagem Dev-1" });
  const wrapperOf = (name: string) =>
    screen.getByRole("article", { name }).closest(".react-flow__node") as HTMLElement;
  expect(wrapperOf("Personagem Dev-1").style.transform).toBe("translate(700px,-100px)");
  expect(wrapperOf("Pergaminho Plano").style.transform).toBe("translate(0px,500px)");
});

// CA-CANVAS-05: a line dragged that the engine refuses is gone, and the reason is shown.
test("a connection the engine refuses disappears and the reason shows", async () => {
  fakeServer({
    [`POST ${path}/connections`]: () => refused("different_tables", "x"),
  });
  const { view, notices, changed } = gestures(tableState());
  let done!: Promise<void>;
  act(() => {
    done = view.result.current.connect({
      source: "character:dev",
      target: "character:rev",
      sourceHandle: null,
      targetHandle: null,
    });
  });
  // while the engine thinks, the line is drawn as pending
  await waitFor(() =>
    expect(view.result.current.pending.map((e) => e.source)).toEqual(["character:dev"]),
  );
  await act(() => done);
  expect(view.result.current.pending).toEqual([]);
  expect(notices).toEqual(["Os dois elementos precisam estar na mesma mesa."]);
  expect(changed).not.toHaveBeenCalled();
});

test("a connection the engine accepts is drawn from the state it gives back", async () => {
  const { calls } = fakeServer({ [`POST ${path}/connections`]: () => ({ status: 204 }) });
  const { view, notices, changed } = gestures(tableState());
  await act(() =>
    view.result.current.connect({
      source: "character:dev",
      target: "character:rev",
      sourceHandle: null,
      targetHandle: null,
    }),
  );
  expect(calls[0]?.body).toEqual({ a: dev, b: rev });
  expect(view.result.current.pending).toEqual([]);
  expect(notices).toEqual([]);
  expect(changed).toHaveBeenCalled();
});

// CA-CANVAS-10 and CA-CONX-05: deleting the two lines removes the manual one and refuses the automatic one with its reason.
test("deleting an automatic and a manual connection: the manual goes, the automatic is refused with the reason", async () => {
  const { calls } = fakeServer({
    [`DELETE ${path}/connections`]: (c) => {
      const b = c.body as { a: { id: string } };
      return b.a.id === "gm"
        ? refused("automatic_connection", "x", { reasons: ["mestre"] })
        : { status: 204 };
    },
  });
  const st = tableState();
  const { view, notices, changed } = gestures(st);
  const edges = [
    {
      id: "auto",
      source: "character:gm",
      target: "character:mm",
      data: { kind: "connection" as const },
    },
    {
      id: "manual",
      source: "character:dev",
      target: "character:rev",
      data: { kind: "connection" as const },
    },
  ];
  await act(() => view.result.current.removeEdges(edges as never));
  expect(calls.map((c) => `${c.method} ${JSON.stringify((c.body as { a: object }).a)}`)).toEqual([
    `DELETE {"kind":"character","id":"gm"}`,
    `DELETE {"kind":"character","id":"dev"}`,
  ]);
  expect(notices).toHaveLength(1);
  expect(notices[0]).toContain("Esta conexão é automática");
  expect(notices[0]).toContain("mestre");
  expect(changed).toHaveBeenCalled();
});

test("the refusal is said in the language of the user, and a reason without a text falls back to the engine's words", () => {
  const { Wrapper } = wrap("en");
  const { result } = renderHook(() => useRefusalText(), { wrapper: Wrapper });
  const refusal = (reason: string, message: string) =>
    new ApiError(409, "refused", message, { reason, reasons: ["mestre"] });
  expect(result.current(refusal("automatic_connection", "x"))).toBe(
    "This connection is automatic and lasts as long as its reason does (mestre). It cannot be removed here.",
  );
  expect(result.current(refusal("something_new", "Palavras do motor"))).toBe("Palavras do motor");
  expect(result.current(new ApiError(0, "network", ""))).toContain("Citadel");
});

test("while a mission is drawn, dragging between characters makes a step and asks the engine for nothing", async () => {
  const { calls } = fakeServer({});
  const { Wrapper } = wrap();
  let draft = { ...emptyDraft(), name: "m" };
  const view = renderHook(
    () =>
      useGestures({
        state: tableState(),
        act: actions("loja", "m1"),
        changed: () => {},
        fail: () => {},
        draft,
        setDraft: (d) => {
          draft = d;
        },
        clear: () => {},
        onlyCharacters: () => {},
      }),
    { wrapper: Wrapper },
  );
  await act(() =>
    view.result.current.connect({
      source: "character:dev",
      target: "character:rev",
      sourceHandle: null,
      targetHandle: null,
    }),
  );
  expect(calls).toEqual([]);
  expect(draft).toEqual(addArrow({ ...emptyDraft(), name: "m" }, "Dev-1", "Revisor"));
});

test("an observer sees the canvas but cannot edit: no group, grimoire or pin buttons act", async () => {
  fakeServer({ [`GET ${path}`]: () => ({ body: tableState({ can_edit: false }) }) });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  await screen.findByRole("article", { name: "Personagem Dev-1" });
  expect(screen.queryByRole("button", { name: "Agrupar seleção" })).toBeNull();
  expect((screen.getByRole("button", { name: "Fixar Dev-1" }) as HTMLButtonElement).disabled).toBe(
    true,
  );
});

test("a table that is not there says so", async () => {
  fakeServer({
    [`GET ${path}`]: () => ({ status: 404, body: { error: { code: "not_found", message: "x" } } }),
  });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  expect((await screen.findByRole("alert")).textContent).toContain("Não achei esta mesa");
});

// Live: the socket tells the canvas the table changed, and it reads the state again, with no reload of the page.
test("a change event makes the canvas read the state again", async () => {
  let version = 1;
  fakeServer({
    [`GET ${path}`]: () => ({
      body: tableState({
        characters: tableState().characters.map((c) =>
          c.id === "dev" ? { ...c, state: version === 1 ? "busy" : "idle" } : c,
        ),
      }),
    }),
  });
  let pushed: (e: MessageEvent) => void = () => {};
  const fake = {
    binaryType: "arraybuffer",
    onopen: null as null | ((e: unknown) => void),
    onmessage: null as null | ((e: { data: unknown }) => void),
    onclose: null,
    send: vi.fn(),
    close: vi.fn(),
  };
  const sock = new UiSocket({ url: "ws://x", open: () => fake as never });
  sock.start();
  fake.onopen?.({});
  pushed = (e) => fake.onmessage?.({ data: e.data });
  const { Wrapper } = wrap("pt-BR", sock);
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  const node = await screen.findByRole("article", { name: "Personagem Dev-1" });
  expect(node.textContent).toContain("Trabalhando");
  const subscribed = fake.send.mock.calls.map(
    (c) => JSON.parse(String(c[0])) as { payload: { topics?: string[] } },
  );
  expect(subscribed.some((m) => m.payload.topics?.includes("canvas:loja/m1"))).toBe(true);
  version = 2;
  pushed(
    new MessageEvent("message", {
      data: JSON.stringify({
        id: "e1",
        type: "ui.event",
        ts: "x",
        payload: { topic: "canvas:loja/m1", event: "changed", data: {} },
      }),
    }),
  );
  await waitFor(() =>
    expect(screen.getByRole("article", { name: "Personagem Dev-1" }).textContent).toContain(
      "Ocioso",
    ),
  );
  sock.stop();
});

// CA-CANVAS-11, the part of the screen: the board opens at zoom 100% and no text of a node
// is below the 17 px of the panel; "Ajustar à tela" is a button, not the first state.
test("the board opens at zoom 100%, its text is the size of the panel and Ajustar à tela is a button", async () => {
  fakeServer({ [`GET ${path}`]: () => ({ body: tableState() }) });
  const { Wrapper } = wrap();
  const { container } = render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  await screen.findByRole("article", { name: "Personagem Dev-1" });
  const viewport = container.querySelector(".react-flow__viewport") as HTMLElement;
  expect(viewport.style.transform).toMatch(/scale\(1\)/);
  expect(screen.getByRole("button", { name: "Ajustar à tela" })).toBeTruthy();
  // text-sm and text-xs are below 17 px in this panel: none in the nodes of the board
  for (const node of container.querySelectorAll(".react-flow__node")) {
    expect(node.querySelector(".text-sm, .text-xs")).toBeNull();
  }
});

test("'Em palavras simples' says in a sentence what each character does and waits for", async () => {
  fakeServer({ [`GET ${path}`]: () => ({ body: tableState() }) });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  const words = await screen.findByRole("complementary", { name: "Em palavras simples" });
  expect(words.textContent).toContain("Dev-1 está trabalhando agora.");
  expect(words.textContent).toContain("Revisor espera por Dev-1.");
  expect(words.textContent).toContain("Mestre está parado e pronto para o próximo trabalho.");
});

test("on a phone the board is a list of characters joined by a line, dashed where one waits, each with Ver terminal", async () => {
  const st = tableState();
  st.characters = st.characters.map((c) => (c.id === "dev" ? { ...c, has_terminal: true } : c));
  st.waits = [
    { id: "w1", waiter: "rev", target: "dev", kind: "ask", reason: "Dev-1", deadlock: false },
  ];
  fakeServer({ [`GET ${path}`]: () => ({ body: st }) });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  const list = await screen.findByRole("list", { name: "Personagens da mesa m1" });
  const items = within(list).getAllByRole("listitem");
  expect(items).toHaveLength(4);
  expect(within(list).getByRole("button", { name: "Ver terminal de Dev-1" })).toBeTruthy();
  // the line before the one that waits is dashed
  const lines = within(list).getAllByTestId("board-link");
  expect(lines).toHaveLength(3);
  expect(lines[2]?.className).toContain("border-dashed");
  expect(lines[0]?.className).not.toContain("border-dashed");
});

// The cards of the board and the selection (docs/painel/canvas.md#desenho-do-quadro-por-dentro-m6-unidade-4f).
const withDecide = { ...ME, capabilities: [...ME.capabilities, "decide_approvals"] };

// CA-CANVAS-19: one card per character, with the words of the vocabulary, the mission line and the two buttons.
test("the card of a character has the state in words, the mission line, Abrir terminal and Fixar, and no Digitar", async () => {
  const st = tableState();
  st.characters = st.characters.map((c) =>
    c.id === "dev" ? { ...c, has_terminal: true, state: "waiting", reason: "approval:12" } : c,
  );
  fakeServer({ [`GET ${path}`]: () => ({ body: st }) });
  const { Wrapper } = wrap("pt-BR", null, withDecide);
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  const card = await screen.findByRole("article", { name: "Personagem Dev-1" });
  expect(card.textContent).toContain("Espera a sua decisão");
  expect(card.textContent).toContain("Missão entrega · Etapa 1 de 2");
  expect(within(card).getByRole("button", { name: "Decidir a aprovação" })).toBeTruthy();
  expect(within(card).getByRole("button", { name: "Abrir o terminal de Dev-1" })).toBeTruthy();
  expect(within(card).getByRole("button", { name: "Fixar Dev-1" }).textContent).toContain("Fixar");
  expect(within(card).queryByRole("button", { name: /Digitar/ })).toBeNull();
  // who cannot decide sees the wait without the button
  cleanup();
  const { Wrapper: W2 } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: W2 });
  const plain = await screen.findByRole("article", { name: "Personagem Dev-1" });
  expect(plain.textContent).toContain("Espera uma aprovação");
  expect(within(plain).queryByRole("button", { name: "Decidir a aprovação" })).toBeNull();
});

// CA-CANVAS-13: no loose buttons; a hint, then the bar with the actions in words.
test("with nothing selected there is a hint and no loose group or grimoire button; with cards selected the bar names each action", async () => {
  fakeServer({ [`GET ${path}`]: () => ({ body: tableState() }) });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  await screen.findByRole("article", { name: "Personagem Dev-1" });
  expect(screen.queryByRole("button", { name: "Agrupar seleção" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Salvar seleção como grimório" })).toBeNull();
  expect(screen.getByText(/Clique num cartão para selecionar/)).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Selecionar vários" }));
  fireEvent.click(screen.getByRole("article", { name: "Personagem Dev-1" }));
  const bar = await screen.findByRole("toolbar", { name: "O que fazer com a seleção" });
  expect(bar.textContent).toContain("1 selecionado");
  expect(
    (within(bar).getByRole("button", { name: "Juntar em grupo" }) as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(bar.textContent).toContain("Selecione pelo menos 2 cartões para juntar");
  fireEvent.click(screen.getByRole("article", { name: "Personagem Revisor" }));
  await waitFor(() => expect(bar.textContent).toContain("2 selecionados"));
  expect(
    (within(bar).getByRole("button", { name: "Juntar em grupo" }) as HTMLButtonElement).disabled,
  ).toBe(false);
  expect(within(bar).getByRole("button", { name: "Guardar como grimório" })).toBeTruthy();
  await user.click(within(bar).getByRole("button", { name: "Soltar" }));
  await waitFor(() => expect(screen.queryByRole("toolbar")).toBeNull());
});

// CA-CANVAS-14: group and undo it; only the layout is touched.
test("Juntar em grupo draws a group with a name and Desfazer grupo frees the cards", async () => {
  const { calls } = fakeServer({
    [`GET ${path}`]: () => ({ body: tableState() }),
    [`PUT ${path}/layout`]: () => ({ body: {} }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  await screen.findByRole("article", { name: "Personagem Dev-1" });
  await user.click(screen.getByRole("button", { name: "Selecionar vários" }));
  fireEvent.click(screen.getByRole("article", { name: "Personagem Dev-1" }));
  fireEvent.click(screen.getByRole("article", { name: "Personagem Revisor" }));
  await user.click(await screen.findByRole("button", { name: "Juntar em grupo" }));
  const name = (await screen.findByRole("textbox", { name: "Nome do grupo" })) as HTMLInputElement;
  expect(name.value).toBe("Grupo");
  const saved = calls.filter((c) => c.method === "PUT" && c.path === `${path}/layout`);
  expect(JSON.stringify(saved[0]?.body)).toContain("group:");
  await user.click(screen.getByRole("button", { name: "Desfazer grupo" }));
  await waitFor(() => expect(screen.queryByRole("textbox", { name: "Nome do grupo" })).toBeNull());
  const last = calls.filter((c) => c.method === "PUT" && c.path === `${path}/layout`).pop();
  expect(JSON.stringify(last?.body)).toContain('"remove":["group:');
});

// CA-CANVAS-15: each tab says what it is; the restore plan is said before it is confirmed.
test("the side panel says what each tab is, and a restore tells the renamed names and the missing class first", async () => {
  fakeServer({
    [`GET ${path}`]: () => ({ body: tableState() }),
    [`GET ${path}/rituals`]: () => ({ body: { rituals: [] } }),
    "GET /api/grimoires": () => ({
      body: {
        grimoires: [
          {
            name: "Fluxo de login",
            workspace: "loja",
            table: "m0",
            author: "Maria",
            saved_at: "2026-10-26T15:00:00Z",
            characters: 2,
            scrolls: 1,
            tomes: 0,
            quests: 1,
          },
        ],
      },
    }),
    [`POST ${path}/grimoires/restore`]: () => ({
      body: {
        name: "Fluxo de login",
        renamed: ["Dev-1 → Dev-1 (2)"],
        choose: ["Revisor"],
        done: false,
      },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  await screen.findByRole("article", { name: "Personagem Dev-1" });
  await user.click(
    screen.getAllByRole("button", { name: "Missões, rituais e grimórios" })[0] as HTMLElement,
  );
  const side = await screen.findByRole("complementary", { name: "Missões, rituais e grimórios" });
  expect(side.textContent).toContain("Uma missão é uma sequência de personagens");
  await user.click(within(side).getByRole("tab", { name: /Ritual/ }));
  expect(side.textContent).toContain("Um ritual é uma tarefa que se repete sozinha");
  await user.click(within(side).getByRole("tab", { name: /Grimório/ }));
  expect(side.textContent).toContain("Um grimório é um modelo guardado de parte de uma mesa");
  await user.click(await within(side).findByRole("button", { name: "Restaurar nesta mesa" }));
  expect(await within(side).findByText("Dev-1 → Dev-1 (2)")).toBeTruthy();
  expect(side.textContent).toContain("A classe Revisor não existe nesta campanha.");
});

// CA-CANVAS-16: who cannot edit sees and moves, and every action that changes something says why it is off.
test("an observer sees a read-only hint and every editing action is off with the reason", async () => {
  fakeServer({
    [`GET ${path}`]: () => ({ body: tableState({ can_edit: false }) }),
    [`GET ${path}/rituals`]: () => ({ body: { rituals: [] } }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  await screen.findByRole("article", { name: "Personagem Dev-1" });
  expect(
    screen.getAllByText(/Só Administrador ou acima muda conexões, missões e rituais/).length,
  ).toBeGreaterThan(0);
  expect(screen.queryByRole("button", { name: "Selecionar vários" })).toBeNull();
  expect((screen.getByRole("button", { name: "Fixar Dev-1" }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  await user.click(
    screen.getAllByRole("button", { name: "Missões, rituais e grimórios" })[0] as HTMLElement,
  );
  const side = await screen.findByRole("complementary", { name: "Missões, rituais e grimórios" });
  expect(within(side).queryByRole("button", { name: "Montar missão" })).toBeNull();
  expect(side.textContent).toContain("Só Administrador ou acima muda isto.");
});

// CA-CANVAS-17: the deadlock is told in words and the legend describes the four lines.
test("a deadlock says who waits for whom and what to do, and the legend describes the four lines", async () => {
  fakeServer({
    [`GET ${path}`]: () => ({
      body: tableState({
        waits: [
          { id: "w1", waiter: "rev", target: "dev", kind: "ask", reason: "Dev-1", deadlock: true },
        ],
        deadlocks: [{ text: "Revisor → Dev-1 → Revisor" }],
      }),
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("Revisor → Dev-1 → Revisor");
  expect(alert.textContent).toContain("Peça ao Grão-Mestre para destravar");
  await user.click(screen.getByRole("button", { name: "Como ler o quadro" }));
  const legend = await screen.findByRole("region", { name: "Como ler o quadro" });
  for (const line of [
    "trabalham juntos",
    "ordem da missão",
    "um espera o outro",
    "impasse: um espera o outro e nenhum anda",
  ])
    expect(legend.textContent).toContain(line);
});

// CA-CANVAS-18: on a phone the list has the actions in a menu and a way to select without Shift.
test("on a phone the list offers Selecionar, the menu with Fixar and Conectar, and says grouping is for the computer", async () => {
  const st = tableState();
  st.characters = st.characters.map((c) => (c.id === "dev" ? { ...c, has_terminal: true } : c));
  const { calls } = fakeServer({
    [`GET ${path}`]: () => ({ body: st }),
    [`POST ${path}/connections`]: () => ({ body: {} }),
    "PUT /api/characters/dev/pin": () => ({ body: {} }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { Wrapper } = wrap();
  render(<TableCanvas workspace="loja" table="m1" />, { wrapper: Wrapper });
  const list = await screen.findByRole("list", { name: "Personagens da mesa m1" });
  expect(
    screen.getByText(/Juntar em grupo, arrastar e ligar cartões só no computador/),
  ).toBeTruthy();
  await user.click(within(list).getByRole("button", { name: "Mais ações de Dev-1" }));
  expect(screen.getByText(/Fixar · O personagem não será encerrado sozinho/)).toBeTruthy();
  await user.click(screen.getByText("Conectar a outro personagem…"));
  await user.click(screen.getByRole("button", { name: "Conectar" }));
  await waitFor(() => expect(calls.some((c) => c.path === `${path}/connections`)).toBe(true));
  await user.click(screen.getByRole("button", { name: "Selecionar" }));
  await user.click(screen.getByRole("checkbox", { name: "Selecionar Dev-1" }));
  await user.click(screen.getByRole("checkbox", { name: "Selecionar Revisor" }));
  const bar = await screen.findAllByRole("toolbar", { name: "O que fazer com a seleção" });
  expect(bar[bar.length - 1]?.textContent).toContain("2 selecionados");
  expect(within(bar[bar.length - 1] as HTMLElement).queryByText("Juntar em grupo")).toBeNull();
  expect(
    within(bar[bar.length - 1] as HTMLElement).getByText("Guardar como grimório"),
  ).toBeTruthy();
});
