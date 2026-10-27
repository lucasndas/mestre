import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { fakeServer, fill, ME, renderApp, unauthenticated } from "../test-utils";

afterEach(() => vi.unstubAllGlobals());

const open = { "GET /api/status": () => ({ body: { setup_open: true } }) };
const closed = { "GET /api/status": () => ({ body: { setup_open: false } }) };

test("a Guild with no user opens the setup wizard", async () => {
  fakeServer({ ...open, "GET /api/me": () => unauthenticated });
  renderApp("/");
  expect(await screen.findByRole("heading", { name: "Crie a sua Guilda" })).toBeTruthy();
});

test("without a session the panel goes to the login, and a login opens the panel", async () => {
  let signed = false;
  const { calls } = fakeServer({
    ...closed,
    "GET /api/me": () => (signed ? { body: ME } : unauthenticated),
    "POST /api/login": () => {
      signed = true;
      return { body: ME };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/sessoes");
  await fill(user, await screen.findByLabelText("Nome de login"), "maria");
  await fill(user, screen.getByLabelText("Senha"), "uma senha bem longa");
  await user.click(screen.getByRole("button", { name: "Entrar" }));
  // back to where the browser was going
  await waitFor(() => expect(router.state.location.pathname).toBe("/sessoes"));
  expect(calls.find((c) => c.path === "/api/login")?.body).toEqual({
    login: "maria",
    password: "uma senha bem longa",
  });
});

test("wrong credentials show the text of the code, in the language of the panel", async () => {
  fakeServer({
    ...closed,
    "GET /api/me": () => unauthenticated,
    "POST /api/login": () => ({
      status: 401,
      body: { error: { code: "invalid_credentials", message: "x" } },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/login", "en");
  await fill(user, await screen.findByLabelText("Login name"), "maria");
  await fill(user, screen.getByLabelText("Password"), "wrong password!");
  await user.click(screen.getByRole("button", { name: "Sign in" }));
  expect((await screen.findByRole("alert")).textContent).toBe("Invalid user or password.");
});

test("the language of the account menu is saved", async () => {
  const { calls } = fakeServer({
    ...closed,
    "GET /api/me": () => ({ body: { ...ME, capabilities: ["status"] } }),
    "GET /api/sessions": () => ({ body: { sessions: [ME.session] } }),
    "PUT /api/me/language": (c) => ({
      body: { ...ME, user: { ...ME.user, language: (c.body as { language: string }).language } },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/sessoes");
  expect(await screen.findByRole("heading", { name: "Minhas sessões" })).toBeTruthy();
  // not the Owner: no switch between mine and everyone's
  expect(screen.queryByRole("link", { name: "De todos" })).toBeNull();
  await user.click(screen.getByRole("button", { name: "Menu da conta" }));
  await user.click(await screen.findByRole("menuitemradio", { name: "English" }));
  expect(await screen.findByRole("heading", { name: "My sessions" })).toBeTruthy();
  await waitFor(() =>
    expect(calls.some((c) => c.method === "PUT" && c.path === "/api/me/language")).toBe(true),
  );
});

test("the Owner sees everyone's sessions and the times are in the Guild time zone", async () => {
  fakeServer({
    ...closed,
    "GET /api/me": () => ({ body: ME }),
    "GET /api/admin/sessions": () => ({
      body: {
        sessions: [
          {
            ...ME.session,
            current: false,
            id: "s2",
            user_name: "Ana",
            device: "Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/130.0 Safari/537.36",
          },
        ],
      },
    }),
  });
  renderApp("/sessoes?todas=true");
  expect(await screen.findByText(/Ana · Chrome · Windows/)).toBeTruthy();
  // 2026-10-26T12:00Z is 09:00 in São Paulo
  expect(screen.getByText(/09:00/)).toBeTruthy();
});

test("ending a session asks the server and reloads the list", async () => {
  let ended = false;
  const { calls } = fakeServer({
    ...closed,
    "GET /api/me": () => ({ body: ME }),
    "GET /api/sessions": () => ({
      body: {
        sessions: ended
          ? [ME.session]
          : [
              ME.session,
              {
                ...ME.session,
                id: "s2",
                current: false,
                device:
                  "Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/130.0 Safari/537.36",
              },
            ],
      },
    }),
    "DELETE /api/sessions/s2": () => {
      ended = true;
      return { status: 204 };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/sessoes");
  await user.click(
    await screen.findByRole("button", { name: "Encerrar a sessão de Chrome · Windows" }),
  );
  expect(await screen.findByText("Sessão encerrada.")).toBeTruthy();
  expect(calls.some((c) => c.method === "DELETE" && c.path === "/api/sessions/s2")).toBe(true);
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: /Chrome · Windows/ })).toBeNull(),
  );
});

test("logout goes to the login", async () => {
  let signed = true;
  fakeServer({
    ...closed,
    "GET /api/me": () => (signed ? { body: ME } : unauthenticated),
    "POST /api/logout": () => {
      signed = false;
      return { status: 204 };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/");
  await user.click(await screen.findByRole("button", { name: "Menu da conta" }));
  await user.click(await screen.findByRole("menuitem", { name: "Sair" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
});

test("the setup wizard checks that the two passwords and the two PINs match before asking the server", async () => {
  const { calls } = fakeServer({ ...open, "GET /api/me": () => unauthenticated });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/setup");
  await fill(user, await screen.findByLabelText("Código de configuração"), "ABCDEFGHJKLM");
  await fill(user, screen.getByLabelText("Seu nome"), "Maria");
  await fill(user, screen.getByLabelText("Nome de login"), "maria");
  await fill(user, screen.getByLabelText("Senha (mínimo 12 caracteres)"), "uma senha bem longa");
  await fill(user, screen.getByLabelText("Repita a senha"), "outra senha bem longa");
  await fill(user, screen.getByLabelText("PIN (6 números)"), "123456");
  await fill(user, screen.getByLabelText("Repita o PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Criar a Guilda" }));
  expect((await screen.findByRole("alert")).textContent).toBe("As duas senhas não são iguais.");
  expect(calls.some((c) => c.path === "/api/setup")).toBe(false);
});
