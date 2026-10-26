import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { api } from "../lib/api";
import { fakeServer, renderWithI18n } from "../test-utils";
import { SensitiveButton } from "./PinField";

afterEach(() => vi.unstubAllGlobals());

test("the PIN field appears after the button and goes in the same request", async () => {
  const { calls } = fakeServer({
    "POST /api/users/u1/access-code": () => ({ body: { code: "ABC" } }),
  });
  const user = userEvent.setup();
  renderWithI18n(
    <SensitiveButton
      onConfirm={(pin) => api("POST", "/api/users/u1/access-code", { kind: "first" }, { pin })}
    >
      Gerar código
    </SensitiveButton>,
  );
  expect(screen.queryByLabelText("PIN")).toBeNull();
  expect(calls).toHaveLength(0);
  await user.click(screen.getByRole("button", { name: "Gerar código" }));
  await user.type(screen.getByLabelText("PIN"), "123456");
  await user.click(screen.getByRole("button", { name: "Gerar código" }));
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(calls[0]?.headers["X-Mestre-Pin"]).toBe("123456");
});

test("a wrong PIN shows the translated error and clears the field", async () => {
  fakeServer({
    "POST /api/x": () => ({ status: 403, body: { error: { code: "pin_invalid", message: "m" } } }),
  });
  const user = userEvent.setup();
  renderWithI18n(
    <SensitiveButton onConfirm={(pin) => api("POST", "/api/x", {}, { pin })}>
      Apagar
    </SensitiveButton>,
  );
  await user.click(screen.getByRole("button", { name: "Apagar" }));
  await user.type(screen.getByLabelText("PIN"), "000000");
  await user.click(screen.getByRole("button", { name: "Apagar" }));
  expect((await screen.findByRole("alert")).textContent).toBe("PIN incorreto.");
  expect((screen.getByLabelText("PIN") as HTMLInputElement).value).toBe("");
});
