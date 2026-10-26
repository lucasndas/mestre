import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useCurrentWorkspace } from "./current";

const CREATE = "\u0000create";

/**
 * The selector of workspaces at the top of every screen: only the ones the user may see (the
 * server decides which; the Owner sees all). Creating one (Owner or Administrator) leads to
 * the screen of the workspaces, where the session zero runs.
 */
export function WorkspaceSelector() {
  const { t } = useTranslation("workspace");
  const navigate = useNavigate();
  const { list, slug, canCreate, ready } = useCurrentWorkspace();
  if (!ready || (list.length === 0 && !canCreate)) return null;
  return (
    <div className="flex items-center gap-2">
      <label htmlFor="workspace-selector" className="sr-only">
        {t("selector.label")}
      </label>
      <select
        id="workspace-selector"
        value={slug}
        onChange={(e) => {
          const v = e.target.value;
          if (v === CREATE) void navigate({ to: "/workspaces" });
          else if (v) void navigate({ to: "/workspaces/$workspace", params: { workspace: v } });
        }}
        className="min-h-11 max-w-40 sm:max-w-56 rounded-field border border-line bg-paper px-2"
      >
        {list.length === 0 ? <option value="">{t("selector.none")}</option> : null}
        {list.map((w) => (
          <option key={w.slug} value={w.slug}>
            {w.slug}
          </option>
        ))}
        {canCreate ? <option value={CREATE}>{t("selector.create")}</option> : null}
      </select>
    </div>
  );
}
