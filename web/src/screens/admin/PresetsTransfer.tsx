import { useState } from "react";
import { useTranslation } from "react-i18next";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button, Field } from "../../components/ui";
import type { ImportChoice, ImportPlan, ImportResult } from "../../gen/panel";
import { api } from "../../lib/api";
import { download } from "../../lib/download";
import { errorText } from "../../lib/errors";
import { ChoiceGroup, MultiChoice, NativeSelect } from "./kit";

const ACTIONS = ["skip", "replace", "rename"] as const;

/** Export in YAML or JSON: nothing chosen exports everything. */
export function ExportPanel({ bases, sheets }: { bases: string[]; sheets: string[] }) {
  const { t } = useTranslation("presets");
  const { t: te } = useTranslation("errors");
  const [format, setFormat] = useState("yaml");
  const [pickedBases, setPickedBases] = useState<string[]>([]);
  const [pickedSheets, setPickedSheets] = useState<string[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);
  return (
    <section aria-labelledby="export-title" className="flex max-w-3xl flex-col gap-3">
      <h2 id="export-title" className="text-2xl">
        {t("export.title")}
      </h2>
      <p className="text-sm text-muted">{t("export.hint")}</p>
      <ChoiceGroup
        label={t("export.format")}
        value={format}
        onChange={setFormat}
        options={[
          { value: "yaml", label: t("export.yaml") },
          { value: "json", label: t("export.json") },
        ]}
      />
      <MultiChoice
        label={t("export.bases")}
        value={pickedBases}
        onChange={setPickedBases}
        options={bases}
        empty={t("export.none")}
      />
      <MultiChoice
        label={t("export.sheets")}
        value={pickedSheets}
        onChange={setPickedSheets}
        options={sheets}
        empty={t("export.none")}
      />
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      {done ? <Alert tone="ok">{t("export.done")}</Alert> : null}
      <div>
        <Button
          isDisabled={busy}
          onPress={async () => {
            setBusy(true);
            setError(null);
            setDone(false);
            try {
              const q = new URLSearchParams({ format });
              if (pickedBases.length) q.set("bases", pickedBases.join(","));
              if (pickedSheets.length) q.set("sheets", pickedSheets.join(","));
              await download(`/api/presets/export?${q}`, `mestre-presets.${format}`);
              setDone(true);
            } catch (err) {
              setError(err);
            } finally {
              setBusy(false);
            }
          }}
        >
          {t("export.submit")}
        </Button>
      </div>
    </section>
  );
}

/** Import: the file is checked as a whole, each repeated name gets a choice, and then it is written. */
export function ImportPanel({ onImported }: { onImported: () => Promise<unknown> }) {
  const { t } = useTranslation("presets");
  const { t: te } = useTranslation("errors");
  const [content, setContent] = useState("");
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [choices, setChoices] = useState<ImportChoice[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [busy, setBusy] = useState(false);

  const change = (text: string) => {
    setContent(text);
    setPlan(null);
    setResult(null);
    setError(null);
  };
  const setChoice = (i: number, patch: Partial<ImportChoice>) =>
    setChoices((all) => all.map((c, j) => (j === i ? { ...c, ...patch } : c)));
  const incomplete = choices.some((c) => c.action === "rename" && c.new_name.trim() === "");
  const kindLabel = (k: string) => (k === "base" ? t("kinds.base") : t("kinds.sheet"));

  return (
    <section aria-labelledby="import-title" className="flex max-w-3xl flex-col gap-3">
      <h2 id="import-title" className="text-2xl">
        {t("import.title")}
      </h2>
      <p className="text-sm text-muted">{t("import.hint")}</p>
      <label className="flex flex-col gap-1">
        <span className="text-[0.95em] font-semibold tracking-wide">{t("import.file")}</span>
        <input
          type="file"
          accept=".yaml,.yml,.json,application/json,application/yaml,text/yaml"
          className="min-h-11 text-sm"
          onChange={async (e) => {
            const f = e.target.files?.[0];
            if (f) change(await f.text());
          }}
        />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-[0.95em] font-semibold tracking-wide">{t("import.paste")}</span>
        <textarea
          rows={8}
          spellCheck={false}
          value={content}
          onChange={(e) => change(e.target.value)}
          className="rounded-field border border-line bg-paper px-3 py-2 font-mono text-sm outline-none focus:border-accent focus:ring-3 focus:ring-accent/25"
        />
      </label>
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      {result ? <Alert tone="ok">{t("import.result", { ...result })}</Alert> : null}
      <div>
        <Button
          variant="secondary"
          isDisabled={busy || content.trim() === ""}
          onPress={async () => {
            setBusy(true);
            setError(null);
            setResult(null);
            try {
              const p = await api<ImportPlan>("POST", "/api/presets/import/plan", { content });
              setPlan(p);
              setChoices(
                (p.conflicts ?? []).map((c) => ({
                  kind: c.kind,
                  name: c.name,
                  action: "skip",
                  new_name: "",
                })),
              );
            } catch (err) {
              setPlan(null);
              setError(err);
            } finally {
              setBusy(false);
            }
          }}
        >
          {t("import.check")}
        </Button>
      </div>
      {plan ? (
        <div className="flex flex-col gap-3 rounded-field border border-line bg-surface p-4">
          <p className="font-semibold">
            {t("import.found", { bases: plan.bases, sheets: plan.sheets })}
          </p>
          {(plan.warnings ?? []).length > 0 ? (
            <div className="text-sm">
              <p className="font-semibold">{t("import.warnings")}</p>
              <ul className="list-disc pl-6">
                {plan.warnings.map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
            </div>
          ) : null}
          {choices.length === 0 ? (
            <p className="text-sm text-muted">{t("import.noConflicts")}</p>
          ) : (
            <>
              <p className="text-sm">{t("import.conflicts")}</p>
              {choices.map((c, i) => (
                <div key={`${c.kind}/${c.name}`} className="flex flex-wrap items-end gap-3">
                  <NativeSelect
                    label={t("import.conflictLabel", { kind: kindLabel(c.kind), name: c.name })}
                    value={c.action}
                    onChange={(v) => setChoice(i, { action: v })}
                    options={ACTIONS.map((a) => ({ value: a, label: t(`import.action.${a}`) }))}
                  />
                  {c.action === "rename" ? (
                    <Field
                      label={t("import.newName", { name: c.name })}
                      value={c.new_name}
                      onChange={(v) => setChoice(i, { new_name: v })}
                      autoComplete="off"
                    />
                  ) : null}
                </div>
              ))}
            </>
          )}
          <div>
            <SensitiveButton
              isDisabled={incomplete}
              onConfirm={async (pin) => {
                const r = await api<ImportResult>(
                  "POST",
                  "/api/presets/import",
                  { content, choices },
                  { pin },
                );
                setResult(r);
                setPlan(null);
                setContent("");
                await onImported();
              }}
            >
              {t("import.submit")}
            </SensitiveButton>
          </div>
        </div>
      ) : null}
    </section>
  );
}
