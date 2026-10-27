import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Term } from "../../components/Term";
import { Alert, Button, Field } from "../../components/ui";
import type { DecreeProposal } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { DecreeCard } from "./Decrees";
import { NativeSelect, PageHeader } from "./kit";
import { Area, ceilingQuery, ProposalCard, WorkspacePicker } from "./rulesKit";

type Say = (k: string, o?: object) => string;
const FIELDS = ["files", "commands", "tools", "domains"] as const;

export function Ceiling() {
  const { t } = useTranslation("rules");
  return (
    <section>
      <PageHeader title={t("ceiling.title")} intro={t("ceiling.intro")} />
      <p className="mb-6 max-w-2xl text-sm text-muted">
        <Term name="Teto da campanha" /> · <Term name="Decreto" />
      </p>
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
  const { data, error } = useQuery(ceilingQuery(workspace));
  const [widening, setWidening] = useState(false);
  const [proposal, setProposal] = useState<DecreeProposal | null>(null);
  const [field, setField] = useState<(typeof FIELDS)[number]>("domains");
  const [value, setValue] = useState("");
  const [text, setText] = useState("");
  const [formError, setFormError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });

  return (
    <div className="flex max-w-3xl flex-col gap-5">
      {picker}
      {notice ? <Alert tone="ok">{notice}</Alert> : null}
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data ? (
        <>
          <div>
            <h2 className="mb-2 text-2xl">{t("ceiling.inForce")}</h2>
            {!data.present ? <p className="text-muted">{t("ceiling.noFile")}</p> : null}
            {data.present && !data.valid ? (
              <Alert>{t("ceiling.invalid", { reason: data.error ?? "" })}</Alert>
            ) : null}
            {data.present ? (
              <figure>
                <figcaption className="mb-1 text-sm text-muted">
                  {t("ceiling.fileLabel")}
                </figcaption>
                <pre className="rounded-field border border-line bg-surface p-3 font-mono text-sm break-words whitespace-pre-wrap">
                  {data.text}
                </pre>
              </figure>
            ) : null}
            <p className="mt-2 text-sm text-muted">{t("ceiling.readOnly")}</p>
          </div>
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
            ) : (
              <div>
                <Button variant="secondary" onPress={() => setWidening(true)}>
                  {t("ceiling.widen")}
                </Button>
              </div>
            )
          ) : null}
        </>
      ) : null}
    </div>
  );
}
