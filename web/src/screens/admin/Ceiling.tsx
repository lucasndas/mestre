import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Button, Field } from "../../components/ui";
import type { DecreeProposal } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { DecreeCard } from "./Decrees";
import {
  EmptyState,
  LevelGate,
  LoadError,
  NativeSelect,
  PageHeader,
  StatusStrip,
  VocabLine,
} from "./kit";
import { Area, ceilingQuery, ProposalCard, WorkspacePicker } from "./rulesKit";

type Say = (k: string, o?: object) => string;
const FIELDS = ["files", "commands", "tools", "domains"] as const;

export function Ceiling() {
  const { t } = useTranslation("rules");
  return (
    <section>
      <PageHeader title={t("ceiling.title")} intro={t("ceiling.intro")} />
      <VocabLine terms={["Teto da campanha", "Decreto"]} />
      <WorkspacePicker>
        {(ws, picker) => <CeilingOf key={ws} workspace={ws} picker={picker} />}
      </WorkspacePicker>
    </section>
  );
}

function CeilingOf({ workspace, picker }: { workspace: string; picker: React.ReactNode }) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const say = t as unknown as Say;
  const qc = useQueryClient();
  const { data, error, refetch } = useQuery(ceilingQuery(workspace));
  const [widening, setWidening] = useState(false);
  const [proposal, setProposal] = useState<DecreeProposal | null>(null);
  const [field, setField] = useState<(typeof FIELDS)[number]>("domains");
  const [value, setValue] = useState("");
  const [text, setText] = useState("");
  const [formError, setFormError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });

  const widenButton = data?.can_widen ? (
    !widening && !proposal ? (
      <Button onPress={() => setWidening(true)}>{t("ceiling.widen")}</Button>
    ) : null
  ) : null;

  return (
    <div className="flex max-w-3xl flex-col gap-5">
      {picker}
      {data ? (
        <StatusStrip
          tone={!data.present ? "neutral" : data.valid ? "ok" : "danger"}
          title={
            !data.present
              ? t("ceiling.stripNone")
              : data.valid
                ? t("ceiling.stripOk")
                : t("ceiling.stripBad")
          }
          hint={
            data.widenings.length > 0
              ? t("ceiling.stripWidenings", { count: data.widenings.length })
              : undefined
          }
        >
          {data.present ? (
            <LevelGate need="administrator" label={t("ceiling.widen")}>
              {widenButton}
            </LevelGate>
          ) : null}
        </StatusStrip>
      ) : null}
      {notice ? <Alert tone="ok">{notice}</Alert> : null}
      {error ? (
        <LoadError what={t("loadWhat.ceiling")} error={error} onRetry={() => void refetch()} />
      ) : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data ? (
        <>
          {!data.present ? (
            <EmptyState title={t("ceiling.emptyTitle")} why={t("ceiling.noFile")}>
              <LevelGate need="administrator" label={t("ceiling.widen")}>
                {widenButton}
              </LevelGate>
            </EmptyState>
          ) : (
            <div>
              {!data.valid ? (
                <Alert>{t("ceiling.invalid", { reason: data.error ?? "" })}</Alert>
              ) : null}
              <details className="mt-2">
                <summary className="min-h-11 cursor-pointer py-2 font-semibold">
                  {t("ceiling.details")}
                </summary>
                <figure>
                  <figcaption className="mb-1 text-sm text-muted">
                    {t("ceiling.fileLabel")}
                  </figcaption>
                  <pre className="overflow-x-auto rounded-field border border-line bg-surface p-3 font-mono text-sm break-words whitespace-pre-wrap">
                    {data.text}
                  </pre>
                </figure>
              </details>
              <p className="mt-2 text-sm text-muted">{t("ceiling.readOnly")}</p>
            </div>
          )}
          {data.widenings.length > 0 ? (
            <div>
              <h2 className="mb-2 text-2xl">{t("ceiling.widenings")}</h2>
              <ul className="flex flex-col gap-3">
                {data.widenings.map((d) => (
                  <li key={d.number}>
                    <DecreeCard
                      d={d}
                      onDone={async () => {
                        setNotice(t("decrees.revoked", { number: d.number }));
                        await refresh();
                      }}
                    />
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {data.can_widen ? (
            proposal ? (
              <ProposalCard
                proposal={proposal}
                onCancel={() => setProposal(null)}
                onConfirmed={async () => {
                  setProposal(null);
                  setWidening(false);
                  setValue("");
                  setText("");
                  setNotice(t("ceiling.widened"));
                  await refresh();
                }}
              />
            ) : widening ? (
              <form
                aria-label={t("ceiling.widenTitle")}
                className="flex max-w-2xl flex-col gap-4 rounded-field border border-line bg-surface p-5"
                onSubmit={async (e) => {
                  e.preventDefault();
                  setBusy(true);
                  setFormError(null);
                  try {
                    setProposal(
                      await api<DecreeProposal>(
                        "POST",
                        `/api/workspaces/${encodeURIComponent(workspace)}/ceiling/widen`,
                        {
                          field,
                          value,
                          text,
                        },
                      ),
                    );
                  } catch (err) {
                    setFormError(err);
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                <h2 className="text-xl">{t("ceiling.widenTitle")}</h2>
                <p className="text-sm text-muted">{t("ceiling.widenHint")}</p>
                <NativeSelect
                  label={t("ceiling.field")}
                  value={field}
                  onChange={(v) => setField(v as (typeof FIELDS)[number])}
                  options={FIELDS.map((f) => ({
                    value: f,
                    label: say(`decrees.form.fields.${f}`),
                  }))}
                />
                <Field
                  label={t("ceiling.value")}
                  description={t("decrees.form.valueHint")}
                  value={value}
                  onChange={setValue}
                />
                <Area
                  label={t("decrees.form.order")}
                  description={t("ceiling.orderHint")}
                  value={text}
                  onChange={setText}
                />
                {formError ? <Alert>{errorText(te, formError)}</Alert> : null}
                <div className="flex gap-3">
                  <Button
                    type="submit"
                    isDisabled={busy || value.trim() === "" || text.trim() === ""}
                  >
                    {t("ceiling.widenSubmit")}
                  </Button>
                  <Button variant="quiet" onPress={() => setWidening(false)}>
                    {t("cancel")}
                  </Button>
                </div>
              </form>
            ) : null
          ) : null}
        </>
      ) : null}
    </div>
  );
}
