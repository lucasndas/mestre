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
  const h1 = await screen.findByRole("heading", { level: 1, name: "Login com Google" });
  const head = h1.closest("header") as HTMLElement;
  expect(within(head).getByText("42-login")).toBeTruthy();
  expect(head.textContent).toContain("Trabalhando");
  expect(head.textContent).not.toContain("working");
  expect(within(head).getByText("epico/42-login")).toBeTruthy();
  expect(head.textContent).toMatch(/Última entrega: .*26\/10/);
  expect(within(head).getByRole("button", { name: "Conversar sobre esta mesa" })).toBeTruthy();
});

test("a table without a delivery says so", async () => {
  fakeServer(
    base(view([table({ last_delivery: undefined })]), {
      "GET /api/workspaces/loja/tables/42-login/canvas": () => ({ body: canvas() }),
    }),
  );
  renderApp("/workspaces/loja/mesas/42-login");
  const h1 = await screen.findByRole("heading", { level: 1 });
  await waitFor(() =>
    expect((h1.closest("header") as HTMLElement).textContent).toContain(
      "Última entrega: ainda sem entrega",
    ),
  );
});

test("Quadro and Terminal are the two tabs of the table; the terminal tab opens on a character and is a link of its own", async () => {
  const u = user();
  server();
  const { router } = renderApp("/workspaces/loja/mesas/42-login");
  const tabs = await screen.findByRole("tablist", { name: "Partes da mesa" });
  expect(within(tabs).getByRole("tab", { name: "Quadro" }).getAttribute("aria-selected")).toBe(
    "true",
  );
  await u.click(within(tabs).getByRole("tab", { name: "Terminal" }));
  await waitFor(() => expect(router.state.location.search).toMatchObject({ aba: "terminal" }));
  // two characters have a terminal: the choice is offered, the one that works is open
  const pick = await screen.findByRole("toolbar", { name: "Terminal de qual personagem" });
  expect(within(pick).getByRole("button", { name: "Dev-1" }).getAttribute("aria-pressed")).toBe(
    "true",
  );
  await u.click(within(pick).getByRole("button", { name: "Revisor" }));
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({ aba: "terminal", personagem: "rev" }),
  );
});

test("the terminal page of a character that sits at a table leads to the tab of the table, and the trail names the table", async () => {
  server({
    "GET /api/characters/dev/terminal": () => ({
      body: info({ name: "Dev-1", workspace: "loja", table: "42-login" }),
    }),
  });
  const { router } = renderApp("/terminal/dev");
  await waitFor(() =>
    expect(router.state.location.pathname).toBe("/workspaces/loja/mesas/42-login"),
  );
  expect(router.state.location.search).toMatchObject({ aba: "terminal", personagem: "dev" });
  const trail = await screen.findByRole("navigation", { name: "Você está em" });
  expect(within(trail).getByText("Login com Google")).toBeTruthy();
});
