import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { tableState } from "../canvas/fixtures";
import { info } from "../terminal/fakes";
import { installFakeSocket } from "../test-socket";
import { fakeServer, renderApp } from "../test-utils";
import { base, table, view } from "../workspace/test-fixtures";

// xterm.js needs a browser; what is under test here is the page around the terminal
vi.mock("../terminal/TerminalView", () => ({ TerminalView: () => <div data-testid="terminal" /> }));

// The page of a table: its state and the two parts it has, the board and the terminal
// (docs/painel/canvas.md#canvas-infinito, docs/painel/terminal-no-painel.md)

beforeEach(() => installFakeSocket());
afterEach(() => vi.unstubAllGlobals());

// the page loads its view and its canvas on demand: a busy machine needs more than the default second
const SLOW = { timeout: 8000 };

const user = () => userEvent.setup({ delay: null, pointerEventsCheck: 0 });

const canvas = () =>
  tableState({
    table: "42-login",
    characters: tableState().characters.map((c) => ({
      ...c,
      has_terminal: c.id === "dev" || c.id === "rev",
    })),
  });

const server = (extra: Record<string, unknown> = {}) =>
  fakeServer(
    base(view([table()]), {
      "GET /api/workspaces/loja/tables/42-login/canvas": () => ({ body: canvas() }),
      ...extra,
    }),
  );

test("the header says the title with the identifier, the state in words, the branch and the last delivery", async () => {
  server();
  renderApp("/workspaces/loja/mesas/42-login");
  const h1 = await screen.findByRole("heading", { level: 1, name: "Login com Google" }, SLOW);
  const head = h1.closest("header") as HTMLElement;
  // no chip with the identifier of the table: the title and the issue say it
  expect(within(head).queryByText("42-login")).toBeNull();
  expect(head.textContent).toContain("issue #42");
  expect(head.textContent).toContain("Trabalhando");
  expect(head.textContent).not.toContain("working");
  expect(within(head).getByText("epico/42-login")).toBeTruthy();
  expect(head.textContent).toMatch(/Última entrega: .*26\/10/);
  expect(within(head).getByRole("button", { name: "Conversar sobre esta mesa" })).toBeTruthy();
  expect(within(head).getByRole("button", { name: "Voltar para a campanha" })).toBeTruthy();
});

test("Voltar para a campanha leads to the campaign", async () => {
  const u = user();
  server();
  const { router } = renderApp("/workspaces/loja/mesas/42-login");
  await u.click(await screen.findByRole("button", { name: "Voltar para a campanha" }, SLOW));
  await waitFor(() => expect(router.state.location.pathname).toBe("/workspaces/loja"), SLOW);
});

test("a table without a delivery says so", async () => {
  fakeServer(
    base(view([table({ last_delivery: undefined })]), {
      "GET /api/workspaces/loja/tables/42-login/canvas": () => ({ body: canvas() }),
    }),
  );
  renderApp("/workspaces/loja/mesas/42-login");
  const h1 = await screen.findByRole("heading", { level: 1 }, SLOW);
  await waitFor(
    () =>
      expect((h1.closest("header") as HTMLElement).textContent).toContain(
        "Última entrega: ainda sem entrega",
      ),
    SLOW,
  );
});

test("Quadro and Terminal are the two tabs of the table; the terminal tab opens on a character and is a link of its own", async () => {
  const u = user();
  server();
  const { router } = renderApp("/workspaces/loja/mesas/42-login");
  const tabs = await screen.findByRole("tablist", { name: "Partes da mesa" }, SLOW);
  expect(within(tabs).getByRole("tab", { name: "Quadro" }).getAttribute("aria-selected")).toBe(
    "true",
  );
  await u.click(await within(tabs).findByRole("tab", { name: "Terminal de Dev-1" }, SLOW));
  await waitFor(
    () => expect(router.state.location.search).toMatchObject({ aba: "terminal" }),
    SLOW,
  );
  // the address names the character that works, never an empty one
  expect(router.state.location.search).toMatchObject({ personagem: "dev" });
  // two characters have a terminal: the choice is offered, the one that works is open
  const pick = await screen.findByRole("toolbar", { name: "Terminal de qual personagem" }, SLOW);
  expect(within(pick).getByRole("button", { name: "Dev-1" }).getAttribute("aria-pressed")).toBe(
    "true",
  );
  await u.click(within(pick).getByRole("button", { name: "Revisor" }));
  await waitFor(
    () =>
      expect(router.state.location.search).toMatchObject({ aba: "terminal", personagem: "rev" }),
    SLOW,
  );
});

test("the terminal page of a character that sits at a table leads to the tab of the table, and the trail names the table", async () => {
  server({
    "GET /api/characters/dev/terminal": () => ({
      body: info({ name: "Dev-1", workspace: "loja", table: "42-login" }),
    }),
  });
  const { router } = renderApp("/terminal/dev");
  await waitFor(
    () => expect(router.state.location.pathname).toBe("/workspaces/loja/mesas/42-login"),
    SLOW,
  );
  expect(router.state.location.search).toMatchObject({ aba: "terminal", personagem: "dev" });
  const trail = await screen.findByRole("navigation", { name: "Você está em" }, SLOW);
  expect(within(trail).getByText("Login com Google")).toBeTruthy();
});

test("on a phone the terminal tab brings its window into view, so Digitar is within reach", async () => {
  const scroll = vi.fn();
  const original = Element.prototype.scrollIntoView;
  Element.prototype.scrollIntoView = scroll;
  // the window has its height: the observer says so as soon as it is asked to watch
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(private cb: (e: { contentRect: { height: number } }[]) => void) {}
      observe() {
        this.cb([{ contentRect: { height: 500 } }]);
      }
      disconnect() {}
      unobserve() {}
    },
  );
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query === "(max-width: 767px)",
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
  try {
    server({
      "GET /api/characters/dev/terminal": () => ({
        body: info({ name: "Dev-1", workspace: "loja", table: "42-login" }),
      }),
    });
    renderApp("/workspaces/loja/mesas/42-login?aba=terminal&personagem=dev");
    await screen.findByTestId("terminal", undefined, SLOW);
    await waitFor(() => expect(scroll).toHaveBeenCalledWith({ block: "start" }), SLOW);
  } finally {
    Element.prototype.scrollIntoView = original;
  }
});
