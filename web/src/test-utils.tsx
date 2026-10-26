import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { I18nextProvider } from "react-i18next";
import { vi } from "vitest";
import { makeRouter } from "./app/router";
import { type Language, setupI18n } from "./i18n";

// fill puts text in a field the way a paste does: one input event, not one per
// key. Only the tests that prove the typing itself type key by key.
export async function fill(
  user: ReturnType<typeof userEvent.setup>,
  el: HTMLElement,
  text: string,
) {
  await user.click(el);
  await user.paste(text);
}

export function renderWithI18n(ui: ReactElement, language: Language = "pt-BR") {
  return render(<I18nextProvider i18n={setupI18n(language)}>{ui}</I18nextProvider>);
}

export interface Call {
  method: string;
  path: string;
  body: unknown;
  headers: Record<string, string>;
}

type Handler = (call: Call) => { status?: number; body?: unknown } | undefined;

/** A JSON body as an object; a form (the files of a message) as its fields, the files by name. */
function bodyOf(body: BodyInit | null | undefined): unknown {
  if (!body) return undefined;
  if (body instanceof FormData) {
    const out: Record<string, unknown> = {};
    for (const [k, v] of body.entries()) {
      const val = typeof v === "string" ? v : v.name;
      out[k] = k === "files" ? [...((out[k] as string[]) ?? []), val] : val;
    }
    return out;
  }
  return JSON.parse(String(body));
}

/** A fake server: handlers by "METHOD /path"; calls are recorded; an unknown route is a 404. */
export function fakeServer(handlers: Record<string, Handler>) {
  const calls: Call[] = [];
  const fetchFake = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const method = init?.method ?? "GET";
    const call: Call = {
      method,
      path,
      body: bodyOf(init?.body),
      headers: (init?.headers ?? {}) as Record<string, string>,
    };
    calls.push(call);
    const h = handlers[`${method} ${path}`];
    const out = h?.(call) ?? { status: 404, body: { error: { code: "not_found", message: "x" } } };
    const status = out.status ?? 200;
    return new Response(status === 204 ? null : JSON.stringify(out.body ?? {}), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fetchFake);
  return { calls };
}

export function renderApp(path: string, language: Language = "pt-BR") {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = makeRouter(queryClient, createMemoryHistory({ initialEntries: [path] }));
  const i18n = setupI18n(language);
  const view = render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>,
  );
  return { ...view, router, queryClient, i18n };
}

export const ME = {
  user: {
    id: "u1",
    name: "Maria Souza",
    login: "maria",
    level: "owner",
    level_name: "Dono",
    workspaces: [],
    has_pin: true,
    language: "",
    last_workspace: "",
  },
  session: {
    id: "s1",
    user_id: "u1",
    device: "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0",
    ip: "192.0.2.1",
    created_at: "2026-10-26T12:00:00Z",
    last_used_at: "2026-10-26T12:30:00Z",
    current: true,
  },
  capabilities: ["manage_users", "status"],
  timezone: "America/Sao_Paulo",
};

export const unauthenticated = {
  status: 401,
  body: { error: { code: "unauthenticated", message: "x" } },
};
