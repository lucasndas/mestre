import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Outlet,
  type RouterHistory,
  redirect,
} from "@tanstack/react-router";
import { ApiError } from "../lib/api";
import { meQuery, statusQuery } from "../lib/queries";
import { Audit } from "../screens/admin/Audit";
import { GitHubConnect } from "../screens/admin/GitHubConnect";
import { Secrets } from "../screens/admin/Secrets";
import { Transfer } from "../screens/admin/Transfer";
import { Users } from "../screens/admin/Users";
import { FirstAccess } from "../screens/FirstAccess";
import { Login } from "../screens/Login";
import { Overview } from "../screens/Overview";
import { Password } from "../screens/Password";
import { Sessions } from "../screens/Sessions";
import { Setup } from "../screens/Setup";
import { Shell } from "./Shell";

export interface RouterContext {
  queryClient: QueryClient;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({ component: Outlet });

/** The session of the browser, or null when there is none. */
async function sessionOrNull(qc: QueryClient) {
  try {
    return await qc.fetchQuery({ ...meQuery, staleTime: Number.POSITIVE_INFINITY });
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      qc.removeQueries({ queryKey: meQuery.queryKey });
      return null;
    }
    throw err;
  }
}

// Behind the login: no session goes to the setup wizard (a Guild with no user)
// or to the login, remembering where the browser was going.
const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "app",
  component: Shell,
  beforeLoad: async ({ context, location }) => {
    if (await sessionOrNull(context.queryClient)) return;
    const status = await context.queryClient.fetchQuery(statusQuery);
    if (status.setup_open) throw redirect({ to: "/setup" });
    throw redirect({ to: "/login", search: { next: location.href } });
  },
});

const overviewRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  component: Overview,
});
const passwordRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/senha",
  component: Password,
});
const sessionsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/sessoes",
  validateSearch: (s: Record<string, unknown>): { todas: boolean } => ({
    todas: s.todas === true || s.todas === "true",
  }),
  component: function SessionsPage() {
    const { todas } = sessionsRoute.useSearch();
    return <Sessions all={todas} />;
  },
});

// Administration (docs/painel/administracao.md): the server decides the level of
// each request; the nav only hides what the level cannot do.
const usersRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/usuarios",
  component: Users,
});
const secretsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/segredos",
  component: Secrets,
});
const auditRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/auditoria",
  component: Audit,
});
const githubRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/github",
  component: GitHubConnect,
});
const transferRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/transferencia",
  component: Transfer,
});

// The screens before the login go to the panel when there is a session already.
async function leaveIfSignedIn(qc: QueryClient) {
  if (await sessionOrNull(qc)) throw redirect({ to: "/" });
}

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  validateSearch: (s: Record<string, unknown>): { next?: string } =>
    typeof s.next === "string" ? { next: s.next } : {},
  beforeLoad: async ({ context }) => {
    await leaveIfSignedIn(context.queryClient);
    const status = await context.queryClient.fetchQuery(statusQuery);
    if (status.setup_open) throw redirect({ to: "/setup" });
  },
  component: function LoginPage() {
    return <Login next={loginRoute.useSearch().next} />;
  },
});

const firstAccessRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/primeiro-acesso",
  validateSearch: (s: Record<string, unknown>): { codigo?: string } =>
    typeof s.codigo === "string" ? { codigo: s.codigo } : {},
  beforeLoad: ({ context }) => leaveIfSignedIn(context.queryClient),
  component: function FirstAccessPage() {
    return <FirstAccess code={firstAccessRoute.useSearch().codigo} />;
  },
});

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/setup",
  beforeLoad: async ({ context }) => {
    const status = await context.queryClient.fetchQuery(statusQuery);
    if (!status.setup_open) throw redirect({ to: "/login", search: {} });
  },
  component: Setup,
});

const routeTree = rootRoute.addChildren([
  appRoute.addChildren([
    overviewRoute,
    sessionsRoute,
    passwordRoute,
    usersRoute,
    secretsRoute,
    auditRoute,
    githubRoute,
    transferRoute,
  ]),
  loginRoute,
  firstAccessRoute,
  setupRoute,
]);

export function makeRouter(queryClient: QueryClient, history?: RouterHistory) {
  return createRouter({ routeTree, context: { queryClient }, defaultPreload: false, history });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof makeRouter>;
  }
}
