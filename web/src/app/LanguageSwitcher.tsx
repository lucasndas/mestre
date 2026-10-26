import { ToggleButton, ToggleButtonGroup } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { LANGUAGES } from "../i18n";
import { useChangeLanguage } from "./session";

const labels = { "pt-BR": "PT", en: "EN" } as const;

export function LanguageSwitcher() {
  const { t, i18n } = useTranslation("common");
  const change = useChangeLanguage();
  return (
    <ToggleButtonGroup
      aria-label={t("language.label")}
      selectionMode="single"
      disallowEmptySelection
      selectedKeys={[i18n.language]}
      onSelectionChange={(keys) => {
        const [k] = [...keys];
        if (k === "pt-BR" || k === "en") change(k);
      }}
      className="flex overflow-hidden rounded-field border border-line text-sm font-semibold"
    >
      {LANGUAGES.map((l) => (
        <ToggleButton
          key={l}
          id={l}
          aria-label={l === "en" ? t("language.en") : t("language.pt")}
          className="min-h-9 min-w-10 px-2 text-muted transition-colors data-[selected]:bg-accent data-[selected]:text-accent-ink"
        >
          {labels[l]}
        </ToggleButton>
      ))}
    </ToggleButtonGroup>
  );
}
