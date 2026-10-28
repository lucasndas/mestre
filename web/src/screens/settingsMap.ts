/**
 * The map of Configurações (docs/painel/design.md#estrutura-da-tela): four groups, each screen
 * with the capability that shows it. The index, the breadcrumbs and the nav read this one list;
 * the server decides the level of each request anyway.
 */
export type SettingsPath =
  | "/sessoes"
  | "/senha"
  | "/github"
  | "/usuarios"
  | "/segredos"
  | "/auditoria"
  | "/guilda"
  | "/perfis"
  | "/apps"
  | "/classes"
  | "/bases-e-fichas"
  | "/decretos"
  | "/delegacoes"
  | "/cadeia"
  | "/teto"
  | "/politica"
  | "/instrucoes"
  | "/consumo";

export type SettingsGroupKey = "account" | "people" | "infra" | "rules";

export interface SettingsEntry {
  /** The key of the name and of the sentence in the `shell` catalog (settings.<key>). */
  key: string;
  path: SettingsPath;
  /** The capability that shows it; none means every user. */
  can?: string;
}

export interface SettingsGroup {
  key: SettingsGroupKey;
  entries: SettingsEntry[];
}

export const SETTINGS_GROUPS: SettingsGroup[] = [
  {
    key: "account",
    entries: [
      { key: "sessions", path: "/sessoes" },
      { key: "password", path: "/senha" },
      { key: "github", path: "/github", can: "connect_own_github" },
    ],
  },
  {
    key: "people",
    entries: [
      { key: "users", path: "/usuarios", can: "manage_users" },
      { key: "secrets", path: "/segredos", can: "bases_vault_policy" },
      { key: "audit", path: "/auditoria", can: "activate_classes" },
    ],
  },
  {
    key: "infra",
    entries: [
      { key: "guild", path: "/guilda", can: "manage_machines" },
      { key: "profiles", path: "/perfis", can: "account_profiles" },
      { key: "apps", path: "/apps", can: "approve_workspace_cls" },
      { key: "classes", path: "/classes", can: "approve_workspace_cls" },
      { key: "presets", path: "/bases-e-fichas", can: "bases_vault_policy" },
    ],
  },
  {
    key: "rules",
    entries: [
      { key: "decrees", path: "/decretos", can: "status" },
      { key: "delegations", path: "/delegacoes", can: "status" },
      { key: "chain", path: "/cadeia", can: "status" },
      { key: "ceiling", path: "/teto", can: "status" },
      { key: "policy", path: "/politica", can: "bases_vault_policy" },
      { key: "instructions", path: "/instrucoes", can: "configure_workspace" },
      { key: "usage", path: "/consumo" },
    ],
  },
];

/** The group and entry of an address, or null when it is not a screen of Configurações. */
export function settingsOf(path: string): { group: SettingsGroup; entry: SettingsEntry } | null {
  for (const group of SETTINGS_GROUPS) {
    const entry = group.entries.find((e) => path === e.path || path.startsWith(`${e.path}/`));
    if (entry) return { group, entry };
  }
  return null;
}

export type Section = "home" | "approvals" | "workspaces" | "settings";

/** The item of the menu that holds the address: every screen belongs to one. */
export function sectionOf(path: string): Section {
  if (path === "/" || path === "") return "home";
  // the page that asks to accept the post is reached from the line over the Início
  if (path === "/transferencia") return "home";
  if (path === "/aprovacoes" || path.startsWith("/aprovacoes/")) return "approvals";
  if (path === "/workspaces" || path.startsWith("/workspaces/") || path.startsWith("/terminal/"))
    return "workspaces";
  return "settings";
}
