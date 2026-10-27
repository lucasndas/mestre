import { useTranslation } from "react-i18next";
import { useMe } from "../app/session";
import { Term } from "../components/Term";

export function Overview() {
  const { t } = useTranslation("shell");
  const me = useMe();
  return (
    <section className="max-w-2xl">
      <h1 className="text-3xl">{t("overview.title")}</h1>
      <p className="mt-2 text-muted">
        {t("overview.welcome", { name: me.user.name, level: me.user.level_name })}
      </p>
      <div className="mt-8 rounded-field border border-dashed border-line p-6">
        <h2 className="text-xl">
          <Term name="Campanha" />
        </h2>
        <p className="mt-2 font-semibold">{t("overview.emptyTitle")}</p>
        <p className="text-muted">{t("overview.emptyBody")}</p>
      </div>
      <p className="mt-10 text-sm text-muted">{t("overview.times", { zone: me.timezone })}</p>
    </section>
  );
}
