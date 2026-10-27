import { readFileSync } from "node:fs";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nextProvider } from "react-i18next";
import { afterEach, expect, test, vi } from "vitest";
import { ConnectionContext } from "../app/socket";
import { tableState } from "../canvas/fixtures";
import { characterStatus, missionLine } from "../canvas/status";
import { glossary } from "../gen/glossary";
import type { CanvasCharacter } from "../gen/panel";
import { setupI18n } from "../i18n";
import { fakeServer, ME, renderWithI18n } from "../test-utils";
import { WebglBudget } from "./budget";
import { fakeFactory, fakeFeed, info } from "./fakes";
import { FONT, TerminalView, type TerminalViewProps } from "./TerminalView";
import { type TerminalContext, useTerminalContext } from "./useTerminalContext";

/** Shows what the hook of the Mestre layer gives, for a test to read. */
function Probe({
  info: terminal,
  seen,
}: {
  info: ReturnType<typeof info>;
  seen: { ctx?: TerminalContext };
}) {
  seen.ctx = useTerminalContext(terminal);
  return null;
}

const user = () => userEvent.setup({ delay: null, pointerEventsCheck: 0 });

const character = (over: Partial<CanvasCharacter> = {}): CanvasCharacter => ({
  id: "c1",
  name: "Revisor",
  kind: "character",
  role: "character",
  class: "Revisor",
  machine: "torre-a",
  machine_name: "Torre A",
  state: "idle",
  pinned: false,
  has_terminal: true,
  ...over,
});

const context = (over: Partial<TerminalContext> = {}): TerminalContext => ({
  character: character(),
  workspace: "loja",
  table: "m1",
  paused: false,
  pausedReason: "",
  canPin: true,
  canPause: true,
  pin: async () => {},
  setPaused: async () => {},
  ...over,
});

function setup(props: Partial<TerminalViewProps> = {}) {
  const f = fakeFeed();
  const fac = fakeFactory();
  const view = (p: Partial<TerminalViewProps> = {}) => (
    <TerminalView
      terminalId="T1"
      info={info()}
      feed={f.feed}
      canType
      timeZone="America/Sao_Paulo"
      budget={new WebglBudget()}
      factory={fac.factory}
      {...props}
      {...p}
    />
  );
  const i18n = setupI18n("pt-BR");
  const wrap = (p?: Partial<TerminalViewProps>) => (
    <I18nextProvider i18n={i18n}>{view(p)}</I18nextProvider>
  );
  const r = render(wrap());
  return { ...f, ...fac, ...r, rerender: (p: Partial<TerminalViewProps>) => r.rerender(wrap(p)) };
}

afterEach(() => vi.restoreAllMocks());

// CA-TUI-01
test("CA-TUI-01: the screen opens 200 x 50 at 15 px in the packaged font, and the bar says who, the class, the machine and the state in words", () => {
  const { lives } = setup({ info: info({ state: "busy", busy: true }), context: context() });
  expect(lives[0]?.opened).toMatchObject({ cols: 200, rows: 50, fontSize: FONT.screen });
  const bar = screen.getByRole("toolbar");
  expect(within(bar).getByRole("heading", { name: "Revisor" })).toBeTruthy();
  expect(bar.textContent).toContain("Revisor · Máquina Torre A");
  expect(bar.textContent).toContain("Trabalhando");
  expect(bar.textContent).not.toContain("busy");
  // the font is packaged, with the line height 1.4 and no ligatures
  const css = readFileSync("src/index.css", "utf8");
  expect(css).toContain('@import "@fontsource-variable/jetbrains-mono"');
  expect(css).toMatch(/--font-mono:\s*"JetBrains Mono Variable"/);
  expect(readFileSync("src/terminal/factory.ts", "utf8")).toContain("lineHeight: 1.4");
  expect(readFileSync("package.json", "utf8")).not.toContain("addon-ligatures");
});

// CA-TUI-02
test("CA-TUI-02: Digitar opens the box of messages, Teclado direto turns every key to the agent, and Ctrl+Alt+K lets go without sending the key", async () => {
  const u = user();
  const { typed, ended, lives } = setup();
  const section = screen.getByRole("region", { name: "Terminal de Revisor" });
  expect(section.getAttribute("data-mode")).toBe("read");
  await u.click(screen.getByRole("button", { name: "Digitar" }));
  expect(section.getAttribute("data-mode")).toBe("message");
  expect(section.className).toContain("border-accent");
  expect(screen.getByRole("textbox", { name: "Mensagem para Revisor" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Parar de digitar" })).toBeTruthy();

  await u.click(screen.getByRole("button", { name: "Teclado direto" }));
  expect(section.getAttribute("data-mode")).toBe("keys");
  expect(lives[0]?.typingOn).toBe(true);
  lives[0]?.send("a");
  expect(typed).toEqual(["a"]);

  fireEvent.keyDown(section, { key: "k", ctrlKey: true, altKey: true });
  expect(section.getAttribute("data-mode")).toBe("read");
  expect(lives[0]?.typingOn).toBe(false);
  expect(ended).toEqual(["T1"]);
  lives[0]?.send("k");
  expect(typed).toEqual(["a"]);
});

// CA-TUI-03
test("CA-TUI-03: a busy agent queues the messages, Tirar and Enviar agora work on one, the rest leaves when it is free, and a refusal puts the message back", async () => {
  const u = user();
  const { typed, followers, rerender } = setup({ info: info({ state: "busy", busy: true }) });
  await u.click(screen.getByRole("button", { name: "Digitar" }));
  const box = screen.getByRole("textbox", { name: "Mensagem para Revisor" });
  for (const text of ["primeira", "segunda", "terceira"]) {
    await u.type(box, text);
    await u.click(screen.getByRole("button", { name: "Pôr na fila" }));
  }
  expect(typed).toEqual([]);
  expect(screen.getByText("Na fila (3): vai para Revisor quando ele terminar")).toBeTruthy();

  await u.click(screen.getByRole("button", { name: "Tirar da fila: segunda" }));
  expect(screen.getByText("Na fila (2): vai para Revisor quando ele terminar")).toBeTruthy();
  await u.click(screen.getByRole("button", { name: "Enviar agora: primeira" }));
  expect(typed).toEqual(["primeira\r"]);

  // the character is free: the one that waits leaves by itself
  rerender({ info: info({ state: "idle", busy: false }) });
  await waitFor(() => expect(typed).toEqual(["primeira\r", "terceira\r"]));
  expect(screen.queryByText(/Na fila/)).toBeNull();

  // the Citadel refuses what was sent: it is not lost
  act(() => followers.get("T1")?.refused?.({ code: "unavailable", message: "x" }));
  expect(screen.getByText(/Na fila \(1\)/)).toBeTruthy();
  expect(screen.getByRole("status").textContent).toContain("voltou para a fila");
  expect(screen.getByRole("alert").textContent).toContain("A Cidadela recusou");
});

// CA-TUI-03
test("CA-TUI-03: a message with several lines goes as one paste, and a free agent gets it at once", async () => {
  const u = user();
  const { typed } = setup();
  await u.click(screen.getByRole("button", { name: "Digitar" }));
  const box = screen.getByRole("textbox", { name: "Mensagem para Revisor" });
  await u.type(box, "linha 1{Shift>}{Enter}{/Shift}linha 2{Enter}");
  expect(typed).toEqual(["\x1b[200~linha 1\nlinha 2\x1b[201~\r"]);
});

// CA-TUI-03
test("CA-TUI-03: the direct keyboard of a busy agent asks first in a strip, not in a dialog", async () => {
  const u = user();
  const { lives } = setup({ info: info({ state: "busy", busy: true }) });
  await u.click(screen.getByRole("button", { name: "Digitar" }));
  await u.click(screen.getByRole("button", { name: "Teclado direto" }));
  expect(screen.getByText("O agente está trabalhando")).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(lives[0]?.typingOn).toBe(false);
});

// CA-TUI-04
test("CA-TUI-04: a level that cannot type sees Digitar off, the reason and nothing is typed", async () => {
  const { typed, lives } = setup({ canType: false });
  const type = screen.getByRole("button", { name: "Digitar" });
  expect(type.hasAttribute("disabled")).toBe(true);
  expect(screen.getByText("Somente leitura: seu nível não permite digitar aqui")).toBeTruthy();
  expect(
    screen.getByRole("region", { name: "Terminal de Revisor" }).getAttribute("data-mode"),
  ).toBe("read");
  lives[0]?.send("rm -rf /\r");
  expect(typed).toEqual([]);
});

// CA-TUI-05
test("CA-TUI-05: while the panel reconnects the strip is amber, the screen dim and Digitar off; typing comes back with the connection", () => {
  const f = fakeFeed();
  const { factory } = fakeFactory();
  const tree = (state: "reconnecting" | "open") => (
    <I18nextProvider i18n={setupI18n("pt-BR")}>
      <ConnectionContext.Provider value={state}>
        <TerminalView
          terminalId="T1"
          info={info()}
          feed={f.feed}
          canType
          timeZone="UTC"
          budget={new WebglBudget()}
          factory={factory}
        />
      </ConnectionContext.Provider>
    </I18nextProvider>
  );
  const { rerender } = render(tree("reconnecting"));
  expect(screen.getByText("Reconectando à Cidadela…")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Digitar" }).hasAttribute("disabled")).toBe(true);
  expect(document.querySelector("[data-renderer]")?.className).toContain("opacity-60");
  rerender(tree("open"));
  expect(screen.queryByText("Reconectando à Cidadela…")).toBeNull();
  expect(screen.getByRole("button", { name: "Digitar" }).hasAttribute("disabled")).toBe(false);
  expect(document.querySelector("[data-renderer]")?.className).not.toContain("opacity-60");
});

// CA-TUI-06
test("CA-TUI-06: a machine that lost the signal at 14:02 is grey, says so in the Guild zone and turns Digitar off, until the signal is back", () => {
  const { followers } = setup();
  act(() => followers.get("T1")?.signal?.({ signal: false, since: "2026-10-26T17:02:00Z" }));
  expect(document.querySelector("[data-renderer]")?.getAttribute("data-signal")).toBe("lost");
  expect(screen.getByRole("status").textContent).toContain("Máquina sem sinal desde 14:02.");
  expect(screen.getByRole("button", { name: "Digitar" }).hasAttribute("disabled")).toBe(true);
  expect(screen.getByRole("toolbar").textContent).toContain("Sem sinal");
  act(() => followers.get("T1")?.signal?.({ signal: true }));
  expect(document.querySelector("[data-renderer]")?.getAttribute("data-signal")).toBe("ok");
  expect(screen.getByRole("button", { name: "Digitar" }).hasAttribute("disabled")).toBe(false);
});

// CA-TUI-07
test("CA-TUI-07: an ended character keeps its output, has no typing and offers Ver a mesa", async () => {
  const u = user();
  const seeTable = vi.fn();
  const { lives } = setup({ info: info({ state: "interrupted" }), onSeeTable: seeTable });
  expect(lives[0]?.disposed).toBe(false);
  expect(screen.getByText(/Revisor foi encerrado/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Digitar" }).hasAttribute("disabled")).toBe(true);
  expect(screen.getByRole("toolbar").textContent).toContain("Encerrado");
  await u.click(screen.getByRole("button", { name: "Ver a mesa" }));
  expect(seeTable).toHaveBeenCalledOnce();
});

const waitingForYou = (): TerminalContext => {
  const c = character({ state: "blocked", reason: "permission" });
  return context({
    character: c,
    status: characterStatus(
      c,
      { characters: [c], waits: [] },
      { noSignal: false, canDecide: true },
    ),
  });
};

// CA-TUI-08
test("CA-TUI-08: a character blocked by a permission shows the strip with Decidir em Aprovações and Digitar off", async () => {
  const u = user();
  const decide = vi.fn();
  setup({ context: waitingForYou(), onDecide: decide, canDecide: true });
  expect(screen.getByText(/Revisor espera a sua decisão/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Digitar" }).hasAttribute("disabled")).toBe(true);
  await u.click(screen.getByRole("button", { name: "Decidir em Aprovações" }));
  expect(decide).toHaveBeenCalledOnce();
});

// CA-TUI-08
test("CA-TUI-08: a permission prompt the server refuses at the typing puts the same block up", async () => {
  const u = user();
  const { followers } = setup();
  await u.click(screen.getByRole("button", { name: "Digitar" }));
  act(() => followers.get("T1")?.refused?.({ code: "permission_prompt", message: "x" }));
  expect(screen.getByRole("button", { name: "Decidir em Aprovações" })).toBeTruthy();
  expect(
    screen.getByRole("region", { name: "Terminal de Revisor" }).getAttribute("data-mode"),
  ).toBe("read");
});

// CA-TUI-09
test("CA-TUI-09: the status line has the step, the bars and the approval; without a mission that part is gone", async () => {
  const u = user();
  const decide = vi.fn();
  const c = character({ state: "blocked", reason: "permission" });
  const state = tableState();
  const ctx = context({
    character: c,
    status: characterStatus(
      c,
      { characters: [c], waits: [] },
      { noSignal: false, canDecide: true },
    ),
    mission: {
      quest: "entrega",
      step: 2,
      total: 4,
      bars: ["done", "current", "todo", "todo"],
    },
    delivery: "2026-10-26T17:00:00Z",
  });
  expect(state.characters.length).toBeGreaterThan(0);
  const { unmount } = setup({ context: ctx, onDecide: decide, canDecide: true });
  expect(screen.getByText("Missão entrega · Etapa 2 de 4")).toBeTruthy();
  expect(screen.getByText(/Última entrega/).textContent).toContain("14:00");
  await u.click(screen.getByRole("button", { name: "Decidir a aprovação de Revisor" }));
  expect(decide).toHaveBeenCalledOnce();
  unmount();
  setup({ context: context({ mission: null }) });
  expect(screen.queryByText(/Missão/)).toBeNull();
  expect(screen.queryByText(/Última entrega/)).toBeNull();
});

// CA-TUI-09
test("CA-TUI-09: the mission line is the one of the card of the canvas (same data, same words)", () => {
  const state = tableState();
  const dev = state.characters.find((c) => c.id === "dev");
  expect(dev).toBeTruthy();
  if (!dev) return;
  const line = missionLine(dev, state);
  const mission = line ?? { quest: "x", step: 1, total: 1, bars: ["current" as const] };
  setup({ context: context({ character: dev, mission }), compact: false });
  const text = screen.getByText(/Missão /);
  expect(text.textContent).toBe(
    `Missão ${mission.quest} · Etapa ${mission.step} de ${mission.total}`,
  );
});

// CA-TUI-10
test("CA-TUI-10: in the card the screen follows the end of the output, shows the mode and no bar; far away it is a picture with its label", () => {
  const { lives, unmount } = setup({ compact: true });
  expect(screen.queryByRole("toolbar")).toBeNull();
  expect(screen.getByText("Leitura")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Digitar" })).toBeNull();
  expect(lives[0]?.opened.fontSize).toBe(FONT.card);
  // the window shows the end: the row of the cursor decides where it is scrolled to
  act(() => lives[0]?.moveCursor(30));
  const scroller = document.querySelector("[data-renderer]") as HTMLElement;
  expect(scroller.scrollTop).toBe((30 + 2) * FONT.card * 1.4);
  expect(scroller.className).toContain("nowheel");
  unmount();
  setup({ compact: true, near: false });
  expect(document.querySelector("[data-renderer]")?.getAttribute("data-renderer")).toBe("snapshot");
  expect(screen.getByText("retrato · atualiza a cada 2 s")).toBeTruthy();
});

// CA-TUI-11
test("CA-TUI-11: on a phone the letter is 12 px, the strip offers to wrap lines without changing 200 x 50, the secondary actions are in Mais and Digitar is 48 px", async () => {
  const u = user();
  const { lives } = setup({ phone: true, context: context() });
  expect(lives[0]?.opened).toMatchObject({ cols: 200, rows: 50, fontSize: FONT.phone });
  expect(
    screen.getByRole("region", { name: "Terminal de Revisor" }).getAttribute("data-phone"),
  ).toBe("yes");
  expect(screen.getByText("200 colunas: role para o lado")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Copiar tela" })).toBeNull();
  expect(screen.getByRole("button", { name: "Digitar" }).className).toContain("min-h-12");

  // wrapping is reading only: a text view that breaks lines, the PTY still 200 x 50
  await u.click(screen.getByRole("button", { name: "Quebrar linhas" }));
  expect(document.querySelector("[data-renderer]")?.getAttribute("data-renderer")).toBe("snapshot");
  expect(document.querySelector("pre")?.className).toContain("whitespace-pre-wrap");
  expect(lives.every((l) => l.opened.cols === 200 && l.opened.rows === 50)).toBe(true);
  await u.click(screen.getByRole("button", { name: "Rolar para o lado" }));

  // Ajustar à largura never goes under 11 px and says when the columns do not fit
  await u.click(screen.getByRole("button", { name: "Mais" }));
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(390);
  await u.click(screen.getByRole("button", { name: "Ajustar à largura" }));
  expect(screen.getByRole("status").textContent).toContain(
    "Não cabe: as 200 colunas pedem 1320 px",
  );
  const live = lives[lives.length - 1];
  expect(live?.fontSize).toBe(FONT.min);
});

// CA-TUI-12
test("CA-TUI-12: Pausar mesa needs the reason written, sends it, and the banner shows it; without the right it is not there", async () => {
  const u = user();
  const setPaused = vi.fn(async () => {});
  const { unmount, rerender } = setup({ context: context({ setPaused }) });
  await u.click(screen.getByRole("button", { name: "Pausar mesa" }));
  const send = screen.getByRole("button", { name: "Pausar" });
  expect(send.hasAttribute("disabled")).toBe(true);
  expect(screen.getByText(/Pausar autonomia/)).toBeTruthy();
  await u.type(screen.getByRole("textbox", { name: "Por que pausar?" }), "revisar o PR");
  await u.click(send);
  expect(setPaused).toHaveBeenCalledWith(true, "revisar o PR");
  expect(await screen.findByText("Mesa pausada.")).toBeTruthy();

  rerender({ context: context({ setPaused, paused: true, pausedReason: "revisar o PR" }) });
  expect(screen.getByText("Mesa pausada: revisar o PR")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Retomar mesa" })).toBeTruthy();
  unmount();

  setup({ context: context({ canPause: false, canPin: false }) });
  expect(screen.queryByRole("button", { name: /Pausar mesa|Retomar mesa/ })).toBeNull();
  expect(screen.queryByRole("button", { name: "Fixar" })).toBeNull();
});

// CA-TUI-12
test("CA-TUI-12: Fixar tells what happened and offers Desfazer", async () => {
  const u = user();
  const pin = vi.fn(async () => {});
  setup({ context: context({ pin }) });
  await u.click(screen.getByRole("button", { name: "Fixar" }));
  expect(pin).toHaveBeenLastCalledWith(true);
  expect(screen.getByRole("status").textContent).toContain("Revisor fixado.");
  await u.click(screen.getByRole("button", { name: "Desfazer" }));
  expect(pin).toHaveBeenLastCalledWith(false);
});

// CA-TUI-12
test("CA-TUI-12: the context asks the existing routes: pause with the reason, resume with the reason, pin on the canvas", async () => {
  const state = tableState();
  const dev = state.characters.find((c) => c.id === "dev");
  const { calls } = fakeServer({
    "GET /api/me": () => ({
      body: { ...ME, capabilities: ["status", "pause_buttons", "decide_approvals"] },
    }),
    "GET /api/workspaces/loja/tables/m1/canvas": () => ({ body: { ...state, can_edit: true } }),
    "GET /api/workspaces/loja/view": () => ({
      body: {
        tables: [
          { id: "m1", paused: true, waits: [{ kind: "paused", reason: "Pausada por Ana" }] },
        ],
      },
    }),
    "POST /api/workspaces/loja/tables/m1/pause": () => ({ body: { changed: true } }),
    "POST /api/chain/loja/tables/m1/resume": () => ({ body: {} }),
    "PUT /api/workspaces/loja/tables/m1/canvas/characters/dev/pin": () => ({ body: {} }),
  });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(["me"], { ...ME, capabilities: ["status", "pause_buttons"] });
  const seen: { ctx?: TerminalContext } = {};
  const terminal = info({
    character: "dev",
    workspace: "loja",
    table: "m1",
    state: "busy",
    busy: true,
  });
  render(
    <QueryClientProvider client={qc}>
      <Probe info={terminal} seen={seen} />
    </QueryClientProvider>,
  );
  const ctx = () => seen.ctx;
  await waitFor(() => expect(ctx()?.character?.name).toBe(dev?.name));
  await waitFor(() => expect(ctx()?.paused).toBe(true));
  expect(ctx()?.pausedReason).toBe("Pausada por Ana");
  expect(ctx()?.canPause).toBe(true);
  expect(ctx()?.canPin).toBe(true);
  await ctx()?.setPaused(true, "revisar");
  await ctx()?.setPaused(false, "voltei");
  await ctx()?.pin(true);
  const sent = calls.filter((c) => c.method !== "GET").map((c) => [c.method, c.path, c.body]);
  expect(sent).toEqual([
    ["POST", "/api/workspaces/loja/tables/m1/pause", { reason: "revisar" }],
    ["POST", "/api/chain/loja/tables/m1/resume", { reason: "voltei" }],
    ["PUT", "/api/workspaces/loja/tables/m1/canvas/characters/dev/pin", { pinned: true }],
  ]);
});

// CA-TUI-13
test("CA-TUI-13: A+, A- and Ajustar change only the letter, within 11 and 22 px; the PTY stays 200 x 50 and nothing goes to the server", async () => {
  const u = user();
  const { lives, typed, ended } = setup();
  await u.click(screen.getByRole("button", { name: "Letra maior" }));
  expect(lives[0]?.fontSize).toBe(16);
  await u.click(screen.getByRole("button", { name: "Letra menor" }));
  await u.click(screen.getByRole("button", { name: "Letra menor" }));
  expect(lives[0]?.fontSize).toBe(14);
  for (let i = 0; i < 20; i++) await u.click(screen.getByRole("button", { name: "Letra menor" }));
  expect(lives[0]?.fontSize).toBe(FONT.min);
  for (let i = 0; i < 20; i++) await u.click(screen.getByRole("button", { name: "Letra maior" }));
  expect(lives[0]?.fontSize).toBe(FONT.max);

  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(3000);
  await u.click(screen.getByRole("button", { name: "Ajustar à largura" }));
  expect(lives[0]?.fontSize).toBe(FONT.max);
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(2400);
  await u.click(screen.getByRole("button", { name: "Ajustar à largura" }));
  expect(lives[0]?.fontSize).toBe(19);
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(1500);
  await u.click(screen.getByRole("button", { name: "Ajustar à largura" }));
  expect(lives[0]?.fontSize).toBe(12);
  expect(lives).toHaveLength(1); // the terminal was not made again
  expect(lives[0]?.opened).toMatchObject({ cols: 200, rows: 50 });
  expect(typed).toEqual([]);
  expect(ended).toEqual([]);
});

// CA-TUI-13
test("CA-TUI-13: Copiar tela puts the text of the screen on the clipboard", async () => {
  const u = user();
  const write = vi.fn(async () => {});
  vi.spyOn(navigator.clipboard ?? {}, "writeText").mockImplementation(write);
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText: write },
    configurable: true,
  });
  setup();
  await u.click(screen.getByRole("button", { name: "Copiar tela" }));
  expect(write).toHaveBeenCalledWith("tela copiada");
  expect(await screen.findByText("Tela copiada.")).toBeTruthy();
});

// CA-TUI-14
test("CA-TUI-14: the region has a name, the screen is also text for screen readers, motion is off with prefers-reduced-motion, and the ANSI colors pass 4.5:1", () => {
  const { lives } = setup();
  expect(screen.getByRole("region", { name: "Terminal de Revisor" })).toBeTruthy();
  expect(lives[0]?.opened.readable).toBeFalsy(); // the canvas does not ask for it
  const standalone = setup({ readable: true });
  expect(standalone.lives[standalone.lives.length - 1]?.opened.readable).toBe(true);

  // the pulse of the dot respects prefers-reduced-motion, and so does the cursor blink (factory)
  expect(readFileSync("src/terminal/TerminalBar.tsx", "utf8")).toContain(
    "motion-safe:animate-pulse",
  );
  expect(readFileSync("src/terminal/factory.ts", "utf8")).toContain("prefers-reduced-motion");

  const css = readFileSync("src/index.css", "utf8");
  const token = (name: string) => {
    const m = css.match(new RegExp(`--color-${name}:\\s*(#[0-9a-fA-F]{6})`));
    if (!m?.[1]) throw new Error(`no token ${name}`);
    return m[1];
  };
  const lum = (hex: string) => {
    const [r = 0, g = 0, b = 0] = [1, 3, 5].map((i) => {
      const v = Number.parseInt(hex.slice(i, i + 2), 16) / 255;
      return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const ratio = (a: string, b: string) => {
    const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
    return ((x ?? 0) + 0.05) / ((y ?? 0) + 0.05);
  };
  const bg = token("term-bg");
  const names = ["red", "green", "yellow", "blue", "magenta", "cyan", "white"];
  for (const n of [...names, ...names.map((x) => `bright-${x}`), "bright-black"]) {
    expect(ratio(token(`term-${n}`), bg), `term-${n}`).toBeGreaterThanOrEqual(4.5);
  }
  expect(ratio(token("term-fg"), bg)).toBeGreaterThanOrEqual(4.5);
});

// CA-TUI-15
test("CA-TUI-15: LEITURA, MENSAGEM and TECLADO open the glossary balloon by hover, press pins it, Esc closes it, and there is no question mark", async () => {
  const u = user();
  setup();
  const reading = glossary.Leitura["pt-BR"];
  const word = screen.getByRole("button", { name: "Ajuda sobre Leitura" });
  expect(word.textContent).toBe("Leitura");
  expect(screen.queryByText("?")).toBeNull();
  await u.hover(word);
  const balloon = await screen.findByRole("dialog");
  expect(balloon.textContent).toContain(reading.help);
  expect(balloon.textContent).toContain("No mundo real");
  expect(balloon.textContent).toContain(reading.real);
  await u.unhover(word);
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

  await u.click(word);
  await screen.findByRole("dialog");
  await u.unhover(word);
  expect(screen.getByRole("dialog")).toBeTruthy(); // pinned
  await u.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

  await u.click(screen.getByRole("button", { name: "Digitar" }));
  expect(screen.getByRole("button", { name: "Ajuda sobre Mensagem" })).toBeTruthy();
  // the direct keyboard is an action: the balloon shows on hover only and the press does the action
  const keys = screen.getByRole("button", { name: "Teclado direto" });
  await u.hover(keys);
  expect((await screen.findByRole("dialog")).textContent).toContain(
    glossary["Teclado direto"]["pt-BR"].help,
  );
  await u.click(keys);
  expect(
    screen.getByRole("region", { name: "Terminal de Revisor" }).getAttribute("data-mode"),
  ).toBe("keys");
  expect(screen.getByRole("button", { name: "Ajuda sobre Teclado direto" })).toBeTruthy();
});

// CA-TUI-15
test("CA-TUI-15: the balloon is in English when the panel is", async () => {
  const u = user();
  const f = fakeFeed();
  const { factory } = fakeFactory();
  renderWithI18n(
    <TerminalView
      terminalId="T1"
      info={info()}
      feed={f.feed}
      canType
      timeZone="UTC"
      budget={new WebglBudget()}
      factory={factory}
    />,
    "en",
  );
  await u.hover(screen.getByRole("button", { name: "Help about Reading" }));
  const balloon = await screen.findByRole("dialog");
  expect(balloon.textContent).toContain(glossary.Leitura.en.help);
  expect(balloon.textContent).toContain("In the real world");
});

// CA-TUI-03
test("CA-TUI-03: an escape typed inside a message cannot end the paste early", async () => {
  const { encodeMessage } = await import("./MessageComposer");
  expect(encodeMessage("a\x1b[201~rm -rf /\nb")).toBe("\x1b[200~a[201~rm -rf /\nb\x1b[201~\r");
  expect(encodeMessage("só uma linha\x1b")).toBe("só uma linha\r");
});

// CA-TUI-03
test("CA-TUI-03: a refused key of the direct keyboard never brings an old message back to the queue", async () => {
  const u = user();
  const { typed, followers } = setup();
  await u.click(screen.getByRole("button", { name: "Digitar" }));
  await u.type(screen.getByRole("textbox", { name: "Mensagem para Revisor" }), "deploy{Enter}");
  expect(typed).toEqual(["deploy\r"]);
  await u.click(screen.getByRole("button", { name: "Teclado direto" }));
  act(() => followers.get("T1")?.refused?.({ code: "unavailable", message: "x" }));
  await u.click(screen.getByRole("button", { name: "Parar de digitar" }));
  await u.click(screen.getByRole("button", { name: "Digitar" }));
  expect(typed).toEqual(["deploy\r"]);
  expect(screen.queryByText(/Na fila/)).toBeNull();
});

// CA-TUI-03
test("CA-TUI-03: C1 controls and tabs never reach the agent in a message", async () => {
  const { encodeMessage } = await import("./MessageComposer");
  expect(encodeMessage("a\u009bb\tc")).toBe("ab  c\r");
});

// CA-TUI-04
test("CA-TUI-04: a level that drops while the box is open sends nothing more", async () => {
  const u = user();
  const { typed, rerender } = setup({ info: info({ busy: true, state: "busy" }) });
  await u.click(screen.getByRole("button", { name: "Digitar" }));
  await u.type(screen.getByRole("textbox", { name: "Mensagem para Revisor" }), "x{Enter}");
  rerender({ canType: false, info: info({ busy: false }) });
  expect(typed).toEqual([]);
});
