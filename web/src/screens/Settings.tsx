import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useMe } from "../app/session";
import { PageHeader } from "./admin/kit";
import { SETTINGS_GROUPS } from "./settingsMap";

type Say = (k: string) => string;

/** Configurações (docs/painel/design.md#estrutura-da-tela): four groups, each entry with the sentence of what it does; only what the level can reach. */
export function Settings() {
  const { t } = useTranslation("shell");
  const say = t as unknown as Say;
  const caps = useMe().capabilities;
  const groups = SETTINGS_GROUPS.map((g) => ({
    ...g,
    entries: g.entries.filter((e) => !e.can || caps.includes(e.can)),
  })).filter((g) => g.entries.length > 0);
  return (
    <section>
      <PageHeader title={t("settings.title")} intro={t("settings.intro")} />
      <div className="grid max-w-5xl gap-5 lg:grid-cols-2">
        {groups.map((g) => (
          <section
            key={g.key}
            id={`grupo-${g.key}`}
            aria-labelledby={`grupo-${g.key}-h`}
            className="rounded-field border border-line bg-surface p-4"
          >
            <h2 id={`grupo-${g.key}-h`} className="text-xl">
              {say(`settings.groups.${g.key}.name`)}
            </h2>
            <p className="mb-2 text-sm text-muted">{say(`settings.groups.${g.key}.blurb`)}</p>
            <ul className="flex flex-col">
              {g.entries.map((e) => (
                <li key={e.key} className="border-t border-line first:border-t-0">
                  <Link
                    to={e.path}
                    search={e.path === "/sessoes" ? { todas: false } : undefined}
                    className="block min-h-14 rounded-field px-1 py-2 hover:bg-accent-wash"
                  >
                    <strong className="block">{say(`settings.entries.${e.key}.name`)}</strong>
                    <span className="block text-sm text-muted">
                      {say(`settings.entries.${e.key}.blurb`)}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </section>
  );
}
