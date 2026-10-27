import { useNavigate } from "@tanstack/react-router";
import { Button as AriaButton, Menu, MenuItem, MenuTrigger, Popover } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useCurrentWorkspace } from "./current";

// not a slug: a campaign slug never holds "~"
const ALL = "~all";
const LINK =
  "inline-flex min-h-11 items-center rounded-field px-1.5 text-accent underline underline-offset-4";

/** The campaign of the trail: it opens the switch of campaigns, and choosing the one the user is in does nothing. */
export function CampaignSwitch({ slug, last }: { slug: string; last: boolean }) {
  const { t } = useTranslation("workspace");
  const navigate = useNavigate();
  const { list } = useCurrentWorkspace();
  return (
    <MenuTrigger>
      <AriaButton
        aria-label={t("switch.label", { workspace: slug })}
        aria-current={last ? "page" : undefined}
        className={`${last ? "inline-flex min-h-11 items-center gap-1 rounded-field px-1.5 font-semibold" : `${LINK} gap-1`}`}
      >
        {slug}
        <span aria-hidden="true">▾</span>
      </AriaButton>
      <Popover
        placement="bottom start"
        className="min-w-48 rounded-field border border-line bg-surface p-1 shadow-pop"
      >
        <Menu
          aria-label={t("switch.title")}
          className="outline-none"
          onAction={(k) => {
            if (k === ALL) void navigate({ to: "/workspaces" });
            else if (k !== slug)
              void navigate({ to: "/workspaces/$workspace", params: { workspace: String(k) } });
          }}
        >
          {list.map((w) => (
            <MenuItem
              key={w.slug}
              id={w.slug}
              textValue={w.slug}
              className="flex min-h-11 cursor-pointer items-center justify-between gap-3 rounded-field px-3 outline-none data-[focused]:bg-accent-wash"
            >
              {w.slug}
              {w.slug === slug ? <span aria-hidden="true">✓</span> : null}
            </MenuItem>
          ))}
          <MenuItem
            id={ALL}
            textValue={t("switch.all")}
            className="flex min-h-11 cursor-pointer items-center rounded-field border-t border-line px-3 outline-none data-[focused]:bg-accent-wash"
          >
            {t("switch.all")}
          </MenuItem>
        </Menu>
      </Popover>
    </MenuTrigger>
  );
}
