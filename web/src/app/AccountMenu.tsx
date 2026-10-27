import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  Button as AriaButton,
  Header,
  Menu,
  MenuItem,
  MenuSection,
  MenuTrigger,
  Popover,
  Separator,
} from "react-aria-components";
import { useTranslation } from "react-i18next";
import { LANGUAGES, type Language } from "../i18n";
import { api } from "../lib/api";
import { useChangeLanguage, useMe } from "./session";

const ITEM =
  "flex min-h-11 cursor-pointer items-center justify-between gap-3 rounded-field px-3 outline-none data-[focused]:bg-accent-wash";

/** The menu of the account (docs/painel/design.md#estrutura-da-tela): my account, the language and the exit. */
export function AccountMenu() {
  const { t } = useTranslation("shell");
  const { t: tc, i18n } = useTranslation("common");
  const me = useMe();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const change = useChangeLanguage();
  const logout = async () => {
    try {
      await api("POST", "/api/logout");
    } finally {
      qc.clear();
      void navigate({ to: "/login", search: {} });
    }
  };
  return (
    <MenuTrigger>
      <AriaButton
        aria-label={t("top.menu")}
        className="grid size-11 shrink-0 place-items-center rounded-full bg-accent-wash text-sm font-semibold hover:bg-line"
      >
        {me.user.name.slice(0, 2).toUpperCase()}
      </AriaButton>
      <Popover
        placement="bottom end"
        className="min-w-56 rounded-field border border-line bg-surface p-1 shadow-pop"
      >
        <Menu
          aria-label={t("top.menu")}
          className="outline-none"
          onAction={(k) => {
            if (k === "logout") void logout();
            else if (k === "account") void navigate({ to: "/sessoes", search: { todas: false } });
          }}
        >
          <MenuSection>
            <Header className="px-3 py-1.5 text-sm text-muted">
              {me.user.name} · {me.user.level_name}
            </Header>
            <MenuItem id="account" textValue={t("top.account")} className={ITEM}>
              {t("top.account")}
            </MenuItem>
          </MenuSection>
          <Separator className="my-1 border-t border-line" />
          <MenuSection
            aria-label={tc("language.label")}
            selectionMode="single"
            selectedKeys={[i18n.language]}
            onSelectionChange={(keys) => {
              const [k] = [...keys];
              if (k === "pt-BR" || k === "en") change(k as Language);
            }}
          >
            {LANGUAGES.map((l) => (
              <MenuItem
                key={l}
                id={l}
                textValue={l === "en" ? tc("language.en") : tc("language.pt")}
                className={ITEM}
              >
                {({ isSelected }) => (
                  <>
                    {l === "en" ? tc("language.en") : tc("language.pt")}
                    {isSelected ? <span aria-hidden="true">✓</span> : null}
                  </>
                )}
              </MenuItem>
            ))}
          </MenuSection>
          <Separator className="my-1 border-t border-line" />
          <MenuItem id="logout" textValue={t("top.logout")} className={ITEM}>
            {t("top.logout")}
          </MenuItem>
        </Menu>
      </Popover>
    </MenuTrigger>
  );
}
