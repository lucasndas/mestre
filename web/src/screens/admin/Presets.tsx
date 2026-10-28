import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button } from "../../components/ui";
import type { BaseRow, PresetSaved, SheetRow } from "../../gen/panel";
import { api } from "../../lib/api";
import { Badge, EmptyState, LoadError, PageHeader, StatusStrip, VocabLine } from "./kit";
import { presetsQuery } from "./manage";
import { ExportPanel, ImportPanel } from "./PresetsTransfer";

type Kind = "bases" | "sheets";

// Starting points for a new item; the server checks the name inside the YAML.
const NEW_BASE =
  "name: minha-base\ncommand: claude\nplatforms:\n  - os: linux\nready:\n  mode: delay\n";
const NEW_SHEET = "name: Minha ficha\nbase: claude-code\nagent: claude\n";

interface Editing {
  kind: Kind;
  /** The name in the path: "novo" creates. */
  path: string;
  title: string;
  yaml: string;
}

/** Bases and sheets: the lists, the YAML editor, and the export and import (Owner). */
export function Presets() {
  const { t } = useTranslation("presets");
  const qc = useQueryClient();
  const { data, error, refetch } = useQuery(presetsQuery);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });

  const del = (kind: Kind, name: string) => async (pin: string) => {
    await api("DELETE", `/api/presets/${kind}/${encodeURIComponent(name)}`, undefined, { pin });
    setNotice(t("deleted", { name }));
    setEditing(null);
    await refresh();
  };

  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <VocabLine terms={["Base", "Ficha"]} />
      {error ? <LoadError what={t("what")} error={error} onRetry={() => void refetch()} /> : null}
      {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
      <div className="flex max-w-4xl flex-col gap-8">
        {data && !error ? (
          <StatusStrip
            tone={data.sheets.length === 0 ? "warn" : "ok"}
            title={`${t("strip.bases", { count: data.bases.length })} ${t("strip.and")} ${t("strip.sheets", { count: data.sheets.length })}`}
            hint={data.sheets.length === 0 ? t("strip.noSheets") : t("strip.hint")}
          >
            <Button
              onPress={() =>
                setEditing({
                  kind: "sheets",
                  path: "novo",
                  title: t("sheets.new"),
                  yaml: NEW_SHEET,
                })
              }
            >
              {t("strip.action")}
            </Button>
          </StatusStrip>
        ) : null}
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {editing ? (
          <Editor
            key={`${editing.kind}/${editing.path}`}
            editing={editing}
            onClose={() => setEditing(null)}
            onSaved={async (name) => {
              setNotice(t("saved", { name }));
              await refresh();
            }}
          />
        ) : null}
        {data && !error ? (
          <>
            <BaseList
              bases={data.bases}
              onNew={() =>
                setEditing({ kind: "bases", path: "novo", title: t("bases.new"), yaml: NEW_BASE })
              }
              onEdit={(b) =>
                setEditing({
                  kind: "bases",
                  path: b.name,
                  title: t("edit", { name: b.name }),
                  yaml: b.yaml,
                })
              }
              onDelete={(name) => del("bases", name)}
            />
            <SheetList
              sheets={data.sheets}
              onNew={() =>
                setEditing({
                  kind: "sheets",
                  path: "novo",
                  title: t("sheets.new"),
                  yaml: NEW_SHEET,
                })
              }
              onEdit={(s) =>
                setEditing({
                  kind: "sheets",
                  path: s.name,
                  title: t("edit", { name: s.name }),
                  yaml: s.yaml,
                })
              }
              onDelete={(name) => del("sheets", name)}
            />
            <ExportPanel
              bases={data.bases.map((b) => b.name)}
              sheets={data.sheets.map((s) => s.name)}
            />
            <ImportPanel onImported={refresh} />
          </>
        ) : null}
      </div>
    </section>
  );
}

function Editor({
  editing,
  onClose,
  onSaved,
}: {
  editing: Editing;
  onClose: () => void;
  onSaved: (name: string) => Promise<void>;
}) {
  const { t } = useTranslation("presets");
  const [yaml, setYaml] = useState(editing.yaml);
  const [warnings, setWarnings] = useState<string[]>([]);
  // the editor opens above the lists: bring it into view and tell screen readers where they are
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
    heading.current?.scrollIntoView?.({ block: "nearest" });
  }, []);
  return (
    <div className="flex max-w-3xl flex-col gap-3 rounded-field border border-line bg-surface p-5">
      <h2 ref={heading} tabIndex={-1} className="text-xl outline-none">
        {editing.title}
      </h2>
      <p className="text-sm text-muted">{t("editor.hint")}</p>
      <label className="flex flex-col gap-1">
        <span className="text-[0.95em] font-semibold tracking-wide">{t("editor.yaml")}</span>
        <textarea
          rows={14}
          spellCheck={false}
          value={yaml}
          onChange={(e) => setYaml(e.target.value)}
          className="rounded-field border border-line bg-paper px-3 py-2 font-mono text-sm outline-none focus:border-accent focus:ring-3 focus:ring-accent/25"
        />
      </label>
      {warnings.length > 0 ? (
        <Alert tone="ok">
          <p className="font-semibold">{t("editor.warnings")}</p>
          <ul className="list-disc pl-6">
            {warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </Alert>
      ) : null}
      <div className="flex flex-wrap items-start gap-3">
        <SensitiveButton
          isDisabled={yaml.trim() === ""}
          onConfirm={async (pin) => {
            const r = await api<PresetSaved>(
              "PUT",
              `/api/presets/${editing.kind}/${encodeURIComponent(editing.path)}`,
              { yaml },
              { pin },
            );
            setWarnings(r.warnings ?? []);
            await onSaved(r.name);
            if ((r.warnings ?? []).length === 0) onClose();
          }}
        >
          {t("editor.save")}
        </SensitiveButton>
        <Button variant="quiet" onPress={onClose}>
          {t("editor.close")}
        </Button>
      </div>
    </div>
  );
}

function BaseList({
  bases,
  onNew,
  onEdit,
  onDelete,
}: {
  bases: BaseRow[];
  onNew: () => void;
  onEdit: (b: BaseRow) => void;
  onDelete: (name: string) => (pin: string) => Promise<void>;
}) {
  const { t } = useTranslation("presets");
  return (
    <section aria-labelledby="bases-title" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="bases-title" className="text-2xl">
          {t("bases.title")}
        </h2>
        <Button variant="secondary" onPress={onNew}>
          {t("bases.new")}
        </Button>
      </div>
      {bases.length === 0 ? (
        <EmptyState title={t("bases.emptyTitle")} why={t("bases.empty")} />
      ) : null}
      {bases.length > 0 ? (
        <div className="overflow-x-auto rounded-field border border-line bg-surface">
          <table className="w-full border-collapse text-left">
            <caption className="sr-only">{t("bases.caption")}</caption>
            <thead>
              <tr className="border-b border-line text-sm text-muted">
                <th scope="col" className="py-2 pr-4">
                  {t("bases.name")}
                </th>
                <th scope="col" className="hidden py-2 pr-4 sm:table-cell">
                  {t("bases.command")}
                </th>
                <th scope="col" className="hidden py-2 pr-4 sm:table-cell">
                  {t("bases.ready")}
                </th>
                <th scope="col" className="hidden py-2 pr-4 sm:table-cell">
                  {t("bases.platforms")}
                </th>
                <th scope="col" className="py-2" />
              </tr>
            </thead>
            <tbody>
              {bases.map((b) => (
                <tr key={b.name} className="border-b border-line align-top">
                  <th scope="row" className="px-3 py-2 pr-4 font-semibold">
                    {b.name} {b.builtin ? <Badge>{t("bases.builtin")}</Badge> : null}
                    <span className="mt-1 block font-mono text-sm font-normal text-muted sm:hidden">
                      {b.command} · {b.platforms.join(", ")}
                    </span>
                  </th>
                  <td className="hidden py-2 pr-4 font-mono text-sm sm:table-cell">{b.command}</td>
                  <td className="hidden py-2 pr-4 text-sm sm:table-cell">{b.ready}</td>
                  <td className="hidden py-2 pr-4 sm:table-cell">{b.platforms.join(", ")}</td>
                  <td className="py-2">
                    <Actions
                      name={b.name}
                      builtin={b.builtin}
                      onEdit={() => onEdit(b)}
                      onDelete={onDelete(b.name)}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}

function SheetList({
  sheets,
  onNew,
  onEdit,
  onDelete,
}: {
  sheets: SheetRow[];
  onNew: () => void;
  onEdit: (s: SheetRow) => void;
  onDelete: (name: string) => (pin: string) => Promise<void>;
}) {
  const { t } = useTranslation("presets");
  return (
    <section aria-labelledby="sheets-title" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="sheets-title" className="text-2xl">
          {t("sheets.title")}
        </h2>
        <Button variant="secondary" onPress={onNew}>
          {t("sheets.new")}
        </Button>
      </div>
      {sheets.length === 0 ? (
        <EmptyState title={t("sheets.emptyTitle")} why={t("sheets.empty")} />
      ) : null}
      {sheets.length > 0 ? (
        <div className="overflow-x-auto rounded-field border border-line bg-surface">
          <table className="w-full border-collapse text-left">
            <caption className="sr-only">{t("sheets.caption")}</caption>
            <thead>
              <tr className="border-b border-line text-sm text-muted">
                <th scope="col" className="py-2 pr-4">
                  {t("sheets.name")}
                </th>
                <th scope="col" className="hidden py-2 pr-4 sm:table-cell">
                  {t("sheets.base")}
                </th>
                <th scope="col" className="hidden py-2 pr-4 sm:table-cell">
                  {t("sheets.agent")}
                </th>
                <th scope="col" className="hidden py-2 pr-4 sm:table-cell">
                  {t("sheets.class")}
                </th>
                <th scope="col" className="hidden py-2 pr-4 sm:table-cell">
                  {t("sheets.account")}
                </th>
                <th scope="col" className="py-2" />
              </tr>
            </thead>
            <tbody>
              {sheets.map((s) => (
                <tr key={s.name} className="border-b border-line align-top">
                  <th scope="row" className="py-2 pr-4 font-semibold">
                    {s.name}
                    {s.incomplete ? (
                      <span className="mt-1 block">
                        <Badge tone="warn">{t("sheets.incomplete", { what: s.incomplete })}</Badge>
                      </span>
                    ) : null}
                    <span className="mt-1 block text-sm font-normal text-muted sm:hidden">
                      {s.base} · {s.agent}
                    </span>
                  </th>
                  <td className="hidden py-2 pr-4 sm:table-cell">{s.base}</td>
                  <td className="hidden py-2 pr-4 sm:table-cell">{s.agent}</td>
                  <td className="hidden py-2 pr-4 sm:table-cell">{s.class || t("sheets.none")}</td>
                  <td className="hidden py-2 pr-4 sm:table-cell">
                    {s.account || t("sheets.none")}
                  </td>
                  <td className="py-2">
                    <Actions name={s.name} onEdit={() => onEdit(s)} onDelete={onDelete(s.name)} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  );
}

function Actions({
  name,
  builtin = false,
  onEdit,
  onDelete,
}: {
  name: string;
  builtin?: boolean;
  onEdit: () => void;
  onDelete: (pin: string) => Promise<void>;
}) {
  const { t } = useTranslation("presets");
  return (
    <div className="flex flex-col items-start gap-2">
      <Button variant="secondary" aria-label={t("edit", { name })} onPress={onEdit}>
        {t("editShort")}
      </Button>
      {builtin ? (
        <p className="text-sm text-muted">{t("bases.builtinHint")}</p>
      ) : (
        <SensitiveButton variant="danger" ariaLabel={t("delete", { name })} onConfirm={onDelete}>
          {t("deleteShort")}
        </SensitiveButton>
      )}
    </div>
  );
}
