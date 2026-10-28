import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Button } from "../../components/ui";
import type { Instructions as InstructionsData } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { LoadError, PageHeader, StatusStrip, VocabLine } from "./kit";
import { Area, instructionsQuery, WorkspacePicker } from "./rulesKit";

const bytes = (s: string) => new TextEncoder().encode(s).length;

export function Instructions() {
  const { t } = useTranslation("rules");
  return (
    <section>
      <PageHeader title={t("instructions.title")} intro={t("instructions.intro")} />
      <VocabLine terms={["Grão-Mestre", "Mestre da mesa"]} />
      <WorkspacePicker>
        {(ws, picker) => <InstructionsOf key={ws} workspace={ws} picker={picker} />}
      </WorkspacePicker>
    </section>
  );
}

function InstructionsOf({ workspace, picker }: { workspace: string; picker: React.ReactNode }) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const { data, error, refetch } = useQuery(instructionsQuery(workspace));
  const [drafts, setDrafts] = useState<Partial<Record<Role, string>>>({});
  const [saveError, setSaveError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const filled = data
    ? [data.grand_master, data.table_masters].filter((x) => x.trim() !== "").length
    : 0;
  const text = (r: Role) => drafts[r] ?? data?.[r] ?? "";
  const changed = ROLES.filter((r) => data !== undefined && text(r) !== data[r]);
  const over = ROLES.some((r) => bytes(text(r)) > (data?.max_bytes ?? Number.POSITIVE_INFINITY));

  const save = async () => {
    setBusy(true);
    setSaveError(null);
    setNotice(null);
    try {
      // the server keeps one role per request: the ones that changed go one after the other
      for (const role of changed) {
        const out = await api<InstructionsData>(
          "PUT",
          `/api/workspaces/${encodeURIComponent(workspace)}/instructions`,
          { role, text: text(role) },
        );
        qc.setQueryData(instructionsQuery(workspace).queryKey, out);
        setDrafts((d) => ({ ...d, [role]: undefined }));
      }
      setNotice(t("instructions.saved"));
      await qc.invalidateQueries({ queryKey: ["admin", "audit"] });
    } catch (err) {
      setSaveError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex max-w-4xl flex-col gap-6">
      {data ? (
        <StatusStrip
          tone={filled > 0 ? "ok" : "neutral"}
          title={
            filled > 0
              ? t("instructions.stripSome", { count: filled })
              : t("instructions.stripNone")
          }
          hint={t("instructions.adds")}
        >
          <Button isDisabled={busy || over || changed.length === 0} onPress={() => void save()}>
            {t("instructions.save")}
          </Button>
          <Button
            variant="quiet"
            isDisabled={busy || changed.length === 0}
            onPress={() => setDrafts({})}
          >
            {t("policy.revert")}
          </Button>
        </StatusStrip>
      ) : null}
      {picker}
      {notice ? <Alert tone="ok">{notice}</Alert> : null}
      {saveError ? <Alert>{errorText(te, saveError)}</Alert> : null}
      {error ? (
        <LoadError
          what={t("loadWhat.instructions")}
          why={t("loadWhy.instructions")}
          error={error}
          onRetry={() => void refetch()}
        />
      ) : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      {data
        ? ROLES.map((role) => (
            <Editor
              key={role}
              which={role}
              text={text(role)}
              max={data.max_bytes}
              onChange={(v) => {
                setDrafts((d) => ({ ...d, [role]: v }));
                setNotice(null);
              }}
            />
          ))
        : null}
    </div>
  );
}

const ROLES = ["grand_master", "table_masters"] as const;
type Role = (typeof ROLES)[number];

function Editor({
  which,
  text,
  max,
  onChange,
}: {
  which: Role;
  text: string;
  max: number;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation("rules");
  const size = bytes(text);
  const over = size > max;
  const heading =
    which === "grand_master" ? t("instructions.grandMaster") : t("instructions.tableMasters");
  const hint = t(
    which === "grand_master" ? "instructions.grandMasterHint" : "instructions.tableMastersHint",
  );
  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-2xl">{heading}</h2>
      <Area
        label={heading}
        hideLabel
        value={text}
        onChange={onChange}
        rows={8}
        placeholder={hint}
        invalidText={over ? "over" : undefined}
      />
      <p
        className={`text-sm ${over ? "font-semibold text-danger" : "text-muted"}`}
        role={over ? "alert" : undefined}
      >
        {t("instructions.size", {
          size,
          limit: max >= 1024 && max % 1024 === 0 ? `${max / 1024} KiB` : `${max} bytes`,
        })}
      </p>
    </section>
  );
}
