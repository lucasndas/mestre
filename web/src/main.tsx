import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { makeRouter } from "./app/router";
import "./index.css";
import { setupI18n } from "./i18n";
import { setUnauthenticatedHandler } from "./lib/api";

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing #root");
}

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
const router = makeRouter(queryClient);
const i18n = setupI18n();
document.documentElement.lang = i18n.language;

// Any 401 of a call says the session is gone: forget it and go to the login.
setUnauthenticatedHandler(() => {
  queryClient.clear();
  void router.navigate({ to: "/login", search: { next: router.state.location.href } });
});

createRoot(root).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>
  </StrictMode>,
);
