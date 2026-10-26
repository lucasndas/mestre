import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Outlet,
  type RouterHistory,
  redirect,
} from "@tanstack/react-router";
import { lazy, Suspense } from "react";
import { useTranslation } from "react-i18next";
import { ApiError } from "../lib/api";
import { meQuery, statusQuery } from "../lib/queries";
import { ApprovalHistoryScreen } from "../screens/ApprovalHistory";
import { Approvals } from "../screens/Approvals";
import { Audit } from "../screens/admin/Audit";
import { Ceiling } from "../screens/admin/Ceiling";
import { Chain } from "../screens/admin/Chain";
import { Decrees } from "../screens/admin/Decrees";
import { Delegations } from "../screens/admin/Delegations";
import { GitHubConnect } from "../screens/admin/GitHubConnect";
import { Instructions } from "../screens/admin/Instructions";
import { Policy } from "../screens/admin/Policy";
import { Secrets } from "../screens/admin/Secrets";
import { Transfer } from "../screens/admin/Transfer";
import { Users } from "../screens/admin/Users";
import { FirstAccess } from "../screens/FirstAccess";
import { Login } from "../screens/Login";
import { Overview } from "../screens/Overview";
import { Password } from "../screens/Password";
import { Sessions } from "../screens/Sessions";
import { Setup } from "../screens/Setup";
import { TableScreen } from "../screens/TableScreen";
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
// The rules and the chain (docs/painel/administracao.md): the same, the server decides.
const decreesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/decretos",
  component: Decrees,
});
const delegationsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/delegacoes",
  component: Delegations,
});
const chainRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/cadeia",
  component: Chain,
});
const ceilingRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/teto",
  component: Ceiling,
});
const policyRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/politica",
  component: Policy,
});
const instructionsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/instrucoes",
  component: Instructions,
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
const approvalsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/aprovacoes",
  validateSearch: (s: Record<string, unknown>): { pedido?: number; aviso?: number } => {
    const n = (v: unknown) =>
      Number.isInteger(Number(v)) && Number(v) > 0 ? Number(v) : undefined;
    return { pedido: n(s.pedido), aviso: n(s.aviso) };
  },
  component: function ApprovalsPage() {
    const { pedido, aviso } = approvalsRoute.useSearch();
    return <Approvals request={pedido} notice={aviso} />;
  },
});
const approvalHistoryRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/aprovacoes/historico",
  component: ApprovalHistoryScreen,
});

// xterm.js is most of the weight of the panel: the terminal screen loads when it is opened
const TerminalScreen = lazy(() =>
  import("../screens/TerminalScreen").then((m) => ({ default: m.TerminalScreen })),
);

function TerminalLoading() {
  const { t } = useTranslation("terminal");
  return (
    <p role="status" className="text-muted">
      {t("loading")}
    </p>
  );
}

const terminalRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/terminal/$character",
  component: function TerminalPage() {
    return (
      <Suspense fallback={<TerminalLoading />}>
        <TerminalScreen character={terminalRoute.useParams().character} />
      </Suspense>
    );
  },
});

// The canvas of a table (docs/painel/canvas.md); the notices of the panel link here
const tableRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/workspaces/$workspace/mesas/$table",
  component: function TablePage() {
    const { workspace, table } = tableRoute.useParams();
    return <TableScreen workspace={workspace} table={table} />;
  },
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
    decreesRoute,
    delegationsRoute,
    chainRoute,
    ceilingRoute,
    policyRoute,
    instructionsRoute,
    githubRoute,
    transferRoute,
    terminalRoute,
    approvalsRoute,
    approvalHistoryRoute,
    tableRoute,
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
