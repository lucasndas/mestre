import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Alert } from "../../components/ui";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { PageHeader, transferQuery } from "./kit";

/** A line over the panel for the user who is asked to take the post of Owner. */
export function TransferBanner() {
  const { t } = useTranslation("users");
  const { data } = useQuery({ ...transferQuery, retry: false });
  const tr = data?.transfer;
  if (!tr?.to_me) return null;
  return (
    <div role="status" className="bg-accent-wash px-4 py-1.5 text-center text-sm font-semibold">
      {t("accept.banner", { name: tr.from_name })}{" "}
      <Link to="/transferencia" className="underline">
        {t("accept.bannerLink")}
      </Link>
    </div>
  );
}

/** The user who was asked accepts the post of Owner, with his PIN. */
export function Transfer() {
  const { t } = useTranslation("users");
  const { t: te } = useTranslation("errors");
  const fmt = useFormatDateTime();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { data, error } = useQuery(transferQuery);
  const tr = data?.transfer;
  return (
    <section>
      <PageHeader
        title={t("accept.title")}
        intro={tr?.to_me ? t("accept.ask", { name: tr.from_name, when: fmt(tr.expires_at) }) : ""}
      />
      <div className="flex max-w-xl flex-col gap-4">
        {error ? <Alert>{errorText(te, error)}</Alert> : null}
        {data && !tr?.to_me ? <p className="text-muted">{t("accept.none")}</p> : null}
        {tr?.to_me ? (
          <>
            <p>{t("accept.effect", { name: tr.from_name })}</p>
            <div>
              <SensitiveButton
                variant="danger"
                onConfirm={async (pin) => {
                  await api("POST", "/api/transfer/accept", undefined, { pin });
                  // the sessions of both end with the change: the panel starts over
                  qc.clear();
                  await navigate({ to: "/login", search: {} });
                }}
              >
                {t("accept.submit")}
              </SensitiveButton>
            </div>
          </>
        ) : null}
      </div>
    </section>
  );
}
