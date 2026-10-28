import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { fakeServer, fill, renderApp, unauthenticated } from "../test-utils";

afterEach(() => vi.unstubAllGlobals());

const closed = { "GET /api/status": () => ({ body: { setup_open: false } }) };
const refuse = (code: string, params: Record<string, unknown> = {}, status = 400) => ({
  ...closed,
  "GET /api/me": () => unauthenticated,
  "POST /api/first-access": () => ({
    status,
    body: { error: { code, message: "x", params } },
  }),
});

async function submit(language: "pt-BR" | "en") {
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/primeiro-acesso", language);
  const pt = language === "pt-BR";
  await fill(
    user,
    await screen.findByLabelText(pt ? "Código de primeiro acesso" : "First-access code"),
    "ABCD2345",
  );
  await fill(
    user,
    screen.getByLabelText(pt ? "Escolha o nome de login" : "Choose a login name"),
    "ana",
  );
  const password = screen.getAllByLabelText(/^(Senha|Password)/);
  await fill(user, password[0] as HTMLElement, "uma senha bem longa");
  await fill(
    user,
    screen.getByLabelText(pt ? "Repita a senha" : "Repeat the password"),
    "uma senha bem longa",
  );
  await user.click(
    screen.getByRole("button", { name: pt ? "Criar meu acesso" : "Create my access" }),
  );
}

const CASES: [string, Record<string, unknown>, number, RegExp, RegExp][] = [
  [
    "invalid_code",
    {},
    400,
    /vencido \(vale 24 horas\)[\s\S]*Peça outro/,
    /expired \(it lasts 24 hours\)[\s\S]*Ask the Guildmaster/,
  ],
  ["password_too_short", { min: 12 }, 400, /pelo menos 12 caracteres/, /at least 12 characters/],
  ["login_taken", {}, 409, /Escolha outro/, /Choose another/],
  ["rate_limited", { retry_after_seconds: 600 }, 429, /Espere 10 min/, /Wait 10 min/],
];
for (const [code, params, status, pt, en] of CASES) {
  // CA-CRESC-04
  test(`first access: ${code} is told in its own words, with the way out, in both languages`, async () => {
    fakeServer(refuse(code, params, status));
    await submit("pt-BR");
    expect((await screen.findByRole("alert")).textContent).toMatch(pt);
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
    fakeServer(refuse(code, params, status));
    await submit("en");
    expect((await screen.findByRole("alert")).textContent).toMatch(en);
  });
}

test("the code is typed, never carried by the address: ?codigo= fills nothing", async () => {
  fakeServer({ ...closed, "GET /api/me": () => unauthenticated });
  renderApp("/primeiro-acesso?codigo=ABCD2345");
  const field = (await screen.findByLabelText("Código de primeiro acesso")) as HTMLInputElement;
  expect(field.value).toBe("");
  expect(screen.getByText(/não é a sua senha/)).toBeTruthy();
});

test("an internal error with no PIN typed says the PIN is missing", async () => {
  fakeServer(refuse("internal", {}, 500));
  await submit("pt-BR");
  expect((await screen.findByRole("alert")).textContent).toContain("Falta o PIN");
});
