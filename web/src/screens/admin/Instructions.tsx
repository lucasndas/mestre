import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Term } from "../../components/Term";
import { Alert, Button } from "../../components/ui";
import type { Instructions as InstructionsData } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { PageHeader } from "./kit";
import { Area, instructionsQuery, WorkspacePicker } from "./rulesKit";

const bytes = (s: string) => new TextEncoder().encode(s).length;

export function Instructions() {
  const { t } = useTranslation("rules");
  return (
    <section>
      <PageHeader title={t("instructions.title")} intro={t("instructions.intro")} />
      <p className="mb-6 max-w-2xl text-sm text-muted">
        <Term name="Grão-Mestre" /> · <Term name="Mestre da mesa" />
      </p>
      <WorkspacePicker>
        {(ws, picker) => <InstructionsOf key={ws} workspace={ws} picker={picker} />}
      </WorkspacePicker>
    </section>
  );
}

function InstructionsOf({ workspace, picker }: { workspace: string; picker: React.ReactNode }) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const { data, error } = useQuery(instructionsQuery(workspace));
  return (
    <div className="flex max-w-3xl flex-col gap-8">
      {picker}
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data ? (
        <>
          <Editor
            workspace={workspace}
            which="grand_master"
            saved={data.grand_master}
            max={data.max_bytes}
          />
          <Editor
            workspace={workspace}
            which="table_masters"
            saved={data.table_masters}
            max={data.max_bytes}
          />
          <p className="text-sm text-muted">{t("instructions.adds")}</p>
        </>
      ) : null}
    </div>
  );
}

function Editor({
  workspace,
  which,
  saved,
  max,
}: {
  workspace: string;
  which: "grand_master" | "table_masters";
  saved: string;
  max: number;
}) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const [draft, setDraft] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const text = draft ?? saved;
  const size = bytes(text);
  const over = size > max;
  const heading =
    which === "grand_master" ? t("instructions.grandMaster") : t("instructions.tableMasters");
  return (
    <form
      aria-label={heading}
      className="flex flex-col gap-3"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(null);
        try {
          const out = await api<InstructionsData>(
            "PUT",
            `/api/workspaces/${encodeURIComponent(workspace)}/instructions`,
            { role: which, text },
          );
          qc.setQueryData(instructionsQuery(workspace).queryKey, out);
          setDraft(null);
          setNotice(t("instructions.saved"));
          await qc.invalidateQueries({ queryKey: ["admin", "audit"] });
        } catch (err) {
          setError(err);
        } finally {
          setBusy(false);
        }
      }}
    >
      <h2 className="text-2xl">{heading}</h2>
      <Area
        label={t("instructions.textLabel", { who: heading })}
        description={t(
          which === "grand_master"
            ? "instructions.grandMasterHint"
            : "instructions.tableMastersHint",
        )}
        value={text}
        onChange={(v) => {
          setDraft(v);
          setNotice(null);
        }}
        rows={8}
        invalidText={over ? "over" : undefined}
      />
      <p
        className={`text-sm ${over ? "font-semibold text-danger" : "text-muted"}`}
        role={over ? "alert" : undefined}
      >
        {t("instructions.size", { size, max })}
      </p>
      {notice ? <Alert tone="ok">{notice}</Alert> : null}
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      <div className="flex gap-3">
        <Button type="submit" isDisabled={busy || over || text === saved}>
          {t("instructions.save")}
        </Button>
        <Button variant="quiet" isDisabled={busy || draft === null} onPress={() => setDraft(null)}>
          {t("policy.revert")}
        </Button>
      </div>
    </form>
  );
}
