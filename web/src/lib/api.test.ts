import { afterEach, expect, test, vi } from "vitest";
import { setupI18n } from "../i18n";
import { fakeServer } from "../test-utils";
import { ApiError, api, setUnauthenticatedHandler } from "./api";
import { errorText } from "./errors";

afterEach(() => {
  vi.unstubAllGlobals();
  setUnauthenticatedHandler(() => {});
});

test("an error carries the code and the params of the server", async () => {
  fakeServer({
    "POST /api/login": () => ({
      status: 429,
      body: {
        error: { code: "login_blocked", params: { retry_after_seconds: 600 }, message: "m" },
      },
    }),
  });
  const err = await api("POST", "/api/login", {}).catch((e: unknown) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect((err as ApiError).code).toBe("login_blocked");
  expect((err as ApiError).params.retry_after_seconds).toBe(600);
});

test("the error text is translated from the code, with the minutes of the wait", async () => {
  const err = new ApiError(429, "login_blocked", "m", { retry_after_seconds: 600 });
  const pt = setupI18n("pt-BR");
  expect(errorText(pt.getFixedT(null, "errors"), err)).toContain("10 min");
  const en = setupI18n("en");
  expect(errorText(en.getFixedT(null, "errors"), err)).toBe(
    "Too many wrong passwords. Try again in 10 min.",
  );
  const unknown = new ApiError(418, "teapot", "Sou um bule");
  expect(errorText(pt.getFixedT(null, "errors"), unknown)).toBe("Sou um bule");
});

test("a 401 of the session goes to the handler, but not the one a login expects", async () => {
  const gone = vi.fn();
  setUnauthenticatedHandler(gone);
  fakeServer({
    "GET /api/x": () => ({
      status: 401,
      body: { error: { code: "unauthenticated", message: "m" } },
    }),
  });
  await api("GET", "/api/x").catch(() => {});
  expect(gone).toHaveBeenCalledOnce();
  await api("GET", "/api/x", undefined, { expect401: true }).catch(() => {});
  expect(gone).toHaveBeenCalledOnce();
});

test("the PIN goes in the header of the very request", async () => {
  const { calls } = fakeServer({
    "POST /api/users/u/access-code": () => ({ body: { code: "A" } }),
  });
  await api("POST", "/api/users/u/access-code", { kind: "first" }, { pin: "123456" });
  expect(calls[0]?.headers["X-Mestre-Pin"]).toBe("123456");
});

test("a network failure is an error of its own", async () => {
  vi.stubGlobal("fetch", () => Promise.reject(new TypeError("down")));
  const err = (await api("GET", "/api/me").catch((e: unknown) => e)) as ApiError;
  expect(err.code).toBe("network");
});
