import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { Alert, Button } from "../../components/ui";
import type { VerificationView } from "../../gen/panel";
import { api } from "../../lib/api";
import { Badge } from "../admin/kit";
import { verificationQuery } from "./queries";
import { refusalText } from "./refusal";

/**
 * The verification commands of a workspace (.mestre/regras/verificacao.yaml): the
 * proposal of the analysis, the version in force, and, for an Administrator, the
 * edit and the confirmation. Only the confirmed version is run
 * (docs/sessao-zero/workspace.md#comandos-de-verificação).
 */
export function Verification({ slug, canConfirm }: { slug: string; canConfirm: boolean }) {
  const { t } = useTranslation("workspaces");
  const { t: te } = useTranslation("errors");
  const q = useQuery(verificationQuery(slug));
  if (q.error) return <Alert>{refusalText(t, te, q.error)}</Alert>;
  if (!q.data) return <p className="text-muted">…</p>;
  return <Editor slug={slug} view={q.data} canConfirm={canConfirm} />;
}

function Editor({
  slug,
  view,
  canConfirm,
}: {
  slug: string;
  view: VerificationView;
  canConfirm: boolean;
}) {
  const { t } = useTranslation("workspaces");
  const { t: te } = useTranslation("errors");
  const fmt = useFormatDateTime();
  const qc = useQueryClient();
  // what is edited starts from the file of the main clone (the proposal), or from
  // the version in force when the file is gone
  const start = view.file || view.confirmed?.doc || "";
  const [doc, setDoc] = useState(start);
  const [error, setError] = useState<unknown>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => setDoc(start), [start]);
  const inForce = view.confirmed && view.file === view.confirmed.doc;

  const confirm = async () => {
    setBusy(true);
    setError(null);
    setDone(false);
    try {
      const next = await api<VerificationView>(
        "PUT",
        `/api/workspaces/${encodeURIComponent(slug)}/verification`,
        { doc },
      );
      qc.setQueryData(verificationQuery(slug).queryKey, next);
      setDone(true);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <p className="flex flex-wrap items-center gap-2 text-sm">
        {view.confirmed ? (
          <Badge tone={view.outside ? "warn" : "ok"}>
            {t("verification.inForce", { who: view.confirmed.by, when: fmt(view.confirmed.at) })}
          </Badge>
        ) : (
          <Badge tone="warn">{t("verification.none")}</Badge>
        )}
      </p>
      {view.confirmed ? null : <p className="text-sm text-muted">{t("verification.noneHint")}</p>}
      {view.outside ? <Alert>{t("verification.outside")}</Alert> : null}
      {view.confirmed ? (
        <details className="text-sm">
          <summary className="cursor-pointer font-semibold">
            {t("verification.confirmedDoc")}
          </summary>
          <pre className="mt-2 overflow-x-auto rounded-field border border-line bg-paper p-3 font-mono">
            {view.confirmed.doc}
          </pre>
        </details>
      ) : null}
      {view.file_error ? (
        <Alert>{t("verification.fileError", { error: view.file_error })}</Alert>
      ) : null}
      {view.commands.length > 0 && !inForce ? (
        <div>
          <p className="text-sm font-semibold">{t("verification.proposed")}</p>
          <ol className="mt-1 list-decimal pl-6 font-mono text-sm">
            {view.commands.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ol>
        </div>
      ) : null}
      {canConfirm ? (
        <div className="flex flex-col gap-2">
          <label className="flex flex-col gap-1">
            <span className="text-[0.95em] font-semibold tracking-wide">
              {t("verification.editor")}
            </span>
            <textarea
              className="min-h-40 rounded-field border border-line bg-paper p-3 font-mono text-sm text-ink outline-none focus:border-accent focus:ring-3 focus:ring-accent/25"
              value={doc}
              spellCheck={false}
              onChange={(e) => setDoc(e.target.value)}
            />
          </label>
          <p className="text-sm text-muted">{t("verification.editorHint")}</p>
          {error ? <Alert>{refusalText(t, te, error)}</Alert> : null}
          {done ? <Alert tone="ok">{t("verification.confirmedNow")}</Alert> : null}
          <div>
            <Button isDisabled={busy || doc.trim() === ""} onPress={() => void confirm()}>
              {t("verification.confirm")}
            </Button>
          </div>
        </div>
      ) : (
        <p className="text-sm text-muted">{t("verification.readOnly")}</p>
      )}
    </div>
  );
}
