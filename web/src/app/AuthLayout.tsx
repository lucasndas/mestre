import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Card } from "../components/ui";
import { LanguageSwitcher } from "./LanguageSwitcher";

export function Brand() {
  const { t } = useTranslation("common");
  return (
    <div className="flex items-center gap-2.5 font-display text-xl">
      <span
        aria-hidden="true"
        className="grid size-7 place-items-center rounded-full bg-accent text-sm font-bold text-accent-ink"
      >
        M
      </span>
      {t("appName")}
    </div>
  );
}

/** The frame of the screens before the login: a card on the paper. */
export function AuthLayout({
  title,
  subtitle,
  children,
  wide = false,
}: {
  title: string;
  subtitle?: string;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <main className="relative grid min-h-dvh place-items-center px-4 py-10">
      <div className="absolute top-4 right-4">
        <LanguageSwitcher />
      </div>
      <Card className={wide ? "max-w-xl" : "max-w-md"}>
        <Brand />
        <h1 className="mt-7 text-3xl">{title}</h1>
        {subtitle ? <p className="mt-1 mb-6 text-muted">{subtitle}</p> : <div className="mb-6" />}
        {children}
      </Card>
    </main>
  );
}
