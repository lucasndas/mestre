import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Button } from "../../components/ui";
import type { PolicyFile } from "../../gen/panel";
import { ApiError, api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { LevelGate, LoadError, PageHeader, StatusStrip, VocabLine } from "./kit";
import { Area, policyQuery, WorkspacePicker } from "./rulesKit";

export function Policy() {
  const { t } = useTranslation("rules");
  return (
    <section>
      <PageHeader title={t("policy.title")} intro={t("policy.intro")} />
      <VocabLine terms={["Cadeia de aprovação", "Delegação"]} />
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
  const { data, error, refetch } = useQuery(policyQuery(workspace));
  const [draft, setDraft] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const text = draft ?? data?.text ?? "";
  const dirty = data !== undefined && text !== data.text;
  const line = lineOfError(saveError);

  const validate = async () => {
    setBusy(true);
    setSaveError(null);
    setNotice(null);
    try {
      await api("POST", `/api/workspaces/${encodeURIComponent(workspace)}/policy/validate`, {
        text,
        version: data?.version ?? "",
      });
      setNotice(t("policy.validOk"));
    } catch (err) {
      setSaveError(err);
    } finally {
      setBusy(false);
    }
  };

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
    <div className="flex max-w-4xl flex-col gap-4">
      {data ? (
        <StatusStrip
          tone={dirty ? "warn" : "ok"}
          title={dirty ? t("policy.stripDirty") : t("policy.stripOk")}
          hint={data.default ? t("policy.default") : t("policy.stripHint")}
        >
          <LevelGate need="owner" label={t("policy.save")}>
            <Button variant="secondary" isDisabled={busy || !dirty} onPress={() => void validate()}>
              {t("policy.validate")}
            </Button>
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
          </LevelGate>
        </StatusStrip>
      ) : null}
      {picker}
      {notice ? <Alert tone="ok">{notice}</Alert> : null}
      {error ? (
        <LoadError
          what={t("loadWhat.policy")}
          why={t("loadWhy.policy")}
          error={error}
          onRetry={() => void refetch()}
        />
      ) : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data ? (
        <>
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
        </>
      ) : null}
    </div>
  );
}
