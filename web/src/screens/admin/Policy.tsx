import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Term } from "../../components/Term";
import { Alert, Button } from "../../components/ui";
import type { PolicyFile } from "../../gen/panel";
import { ApiError, api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { PageHeader } from "./kit";
import { Area, policyQuery, WorkspacePicker } from "./rulesKit";

export function Policy() {
  const { t } = useTranslation("rules");
  return (
    <section>
      <PageHeader title={t("policy.title")} intro={t("policy.intro")} />
      <p className="mb-6 max-w-2xl text-sm text-muted">
        <Term name="Cadeia de aprovação" /> · <Term name="Delegação" />
      </p>
      <WorkspacePicker>
        {(ws, picker) => <PolicyOf key={ws} workspace={ws} picker={picker} />}
      </WorkspacePicker>
    </section>
  );
}

/** The line of the file an error of the server points at, with the text of it. */
function lineOfError(err: unknown): number {
  if (err instanceof ApiError && err.code === "invalid_policy") {
    const n = err.params.line;
    return typeof n === "number" ? n : 0;
  }
  return 0;
}

function PolicyOf({ workspace, picker }: { workspace: string; picker: React.ReactNode }) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const { data, error } = useQuery(policyQuery(workspace));
  const [draft, setDraft] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const text = draft ?? data?.text ?? "";
  const dirty = data !== undefined && text !== data.text;
  const line = lineOfError(saveError);

  const save = async () => {
    if (!data) return;
    setBusy(true);
    setSaveError(null);
    setNotice(null);
    try {
      const saved = await api<PolicyFile>(
        "PUT",
        `/api/workspaces/${encodeURIComponent(workspace)}/policy`,
        {
          text,
          version: data.version,
        },
      );
      qc.setQueryData(policyQuery(workspace).queryKey, saved);
      setDraft(null);
      setNotice(t("policy.saved"));
      await qc.invalidateQueries({ queryKey: ["admin", "audit"] });
    } catch (err) {
      setSaveError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex max-w-3xl flex-col gap-4">
      {picker}
      {notice ? <Alert tone="ok">{notice}</Alert> : null}
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data ? (
        <>
          {data.default ? <Alert tone="ok">{t("policy.default")}</Alert> : null}
          <Area
            label={t("policy.fileLabel")}
            description={t("policy.fileHint")}
            value={text}
            onChange={(v) => {
              setDraft(v);
              setNotice(null);
            }}
            rows={22}
            mono
            invalidText={saveError ? "invalid" : undefined}
          />
          {saveError ? (
            <Alert>
              {line > 0 ? (
                <>
                  <strong>{t("policy.line", { line })}</strong>{" "}
                  <code className="font-mono text-sm">{text.split("\n")[line - 1]?.trim()}</code>
                  <span className="block">{errorText(te, saveError)}</span>
                </>
              ) : (
                errorText(te, saveError)
              )}
              <span className="block text-sm">{t("policy.nothingSaved")}</span>
            </Alert>
          ) : null}
          <div className="flex gap-3">
            <Button isDisabled={busy || !dirty} onPress={() => void save()}>
              {t("policy.save")}
            </Button>
            <Button
              variant="quiet"
              isDisabled={busy || !dirty}
              onPress={() => {
                setDraft(null);
                setSaveError(null);
              }}
            >
              {t("policy.revert")}
            </Button>
          </div>
        </>
      ) : null}
    </div>
  );
}
