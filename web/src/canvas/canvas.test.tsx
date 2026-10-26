import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
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

function wrap(language: "pt-BR" | "en" = "pt-BR", sock: UiSocket | null = null) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(["me"], ME);
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
  expect(devNode.textContent).toContain("ocupado");
  expect(screen.getByRole("article", { name: "Personagem Revisor" }).textContent).toContain(
    "aguardando Dev-1",
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
  expect(node.textContent).toContain("ocupado");
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
      "ocioso",
    ),
  );
  sock.stop();
});
