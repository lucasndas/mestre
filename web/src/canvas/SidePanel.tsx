import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Tab, TabList, TabPanel, Tabs } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { Term } from "../components/Term";
import { Button, Field } from "../components/ui";
import type { CanvasState, GrimoirePlan, RitualInfo } from "../gen/panel";
import { formatDateTime } from "../lib/format";
import { grimoiresQuery, ritualsQuery } from "./api";
import { useCanvasEnv } from "./context";
import { type Draft, emptyDraft, fromQuest, move, remove, toRequest } from "./mission";

export interface SidePanelProps {
  state: CanvasState;
  draft: Draft | null;
  setDraft: (d: Draft | null) => void;
  /** The tab shown; the toolbar opens "missions" when a mission is begun. */
  tab: string;
  setTab: (t: string) => void;
}

const tabClass =
  "min-h-11 cursor-pointer border-b-2 border-transparent px-3 font-semibold outline-none data-selected:border-accent data-focus-visible:ring-2 data-focus-visible:ring-accent";

/** The side panel of the table: missions (draw and adjust), rituals, and the grimoires to restore. */
export function SidePanel({ state, draft, setDraft, tab, setTab }: SidePanelProps) {
  const { t } = useTranslation("canvas");
  return (
    <aside
      aria-label={t("side.title")}
      className="flex max-h-[45%] min-h-0 w-full flex-col border-t border-line bg-surface md:max-h-none md:w-96 md:border-t-0 md:border-l"
    >
      <Tabs
        selectedKey={tab}
        onSelectionChange={(k) => setTab(String(k))}
        className="flex min-h-0 flex-1 flex-col"
      >
        <TabList aria-label={t("side.title")} className="flex flex-wrap border-b border-line">
          <Tab id="missions" className={tabClass}>
            <Term name="Missão" />
          </Tab>
          <Tab id="rituals" className={tabClass}>
            <Term name="Ritual" />
          </Tab>
          <Tab id="grimoires" className={tabClass}>
            <Term name="Grimório" />
          </Tab>
        </TabList>
        <TabPanel id="missions" className="min-h-0 flex-1 overflow-auto p-3">
          <Missions state={state} draft={draft} setDraft={setDraft} />
        </TabPanel>
        <TabPanel id="rituals" className="min-h-0 flex-1 overflow-auto p-3">
          <Rituals state={state} />
        </TabPanel>
        <TabPanel id="grimoires" className="min-h-0 flex-1 overflow-auto p-3">
          <Grimoires state={state} />
        </TabPanel>
      </Tabs>
    </aside>
  );
}

function Missions({
  state,
  draft,
  setDraft,
}: {
  state: CanvasState;
  draft: Draft | null;
  setDraft: (d: Draft | null) => void;
}) {
  const { t } = useTranslation("canvas");
  const env = useCanvasEnv();
  const [busy, setBusy] = useState(false);
  const save = async (run: boolean) => {
    if (!draft) return;
    setBusy(true);
    try {
      const req = toRequest(draft, run);
      if (draft.questId) await env.act.editQuest(draft.questId, req);
      else await env.act.createQuest(req);
      setDraft(null);
      env.changed();
    } catch (err) {
      env.fail(err);
    } finally {
      setBusy(false);
    }
  };

  if (draft) {
    return (
      <section aria-label={t("mission.drawing")} className="flex flex-col gap-3">
        <h2 className="m-0 text-xl">
          {draft.questId ? t("mission.adjust", { name: draft.name }) : t("mission.new")}
        </h2>
        <p className="m-0 text-sm text-muted">{t("mission.how")}</p>
        {draft.questId ? null : (
          <Field
            label={t("mission.name")}
            value={draft.name}
            onChange={(name) => setDraft({ ...draft, name })}
          />
        )}
        {draft.steps.length === 0 ? (
          <p className="m-0 text-muted">{t("mission.noSteps")}</p>
        ) : (
          <ol className="m-0 flex list-none flex-col gap-1 p-0">
            {draft.steps.map((s, i) => (
              <li
                key={s.id}
                className="flex items-center gap-2 rounded-field border border-line bg-paper px-2 py-1"
              >
                <span className="w-6 text-center font-semibold">{i + 1}</span>
                <span className="min-w-0 flex-1 truncate">
                  {s.character}
                  {s.returnTo ? (
                    <span className="text-sm text-muted">
                      {" "}
                      {t("mission.returnsTo", { step: s.returnTo })}
                    </span>
                  ) : null}
                </span>
                <Button
                  variant="quiet"
                  className="min-w-11 px-2"
                  aria-label={t("mission.up", { name: s.character })}
                  isDisabled={i === 0}
                  onPress={() => setDraft(move(draft, s.id, -1))}
                >
                  ↑
                </Button>
                <Button
                  variant="quiet"
                  className="min-w-11 px-2"
                  aria-label={t("mission.down", { name: s.character })}
                  isDisabled={i === draft.steps.length - 1}
                  onPress={() => setDraft(move(draft, s.id, 1))}
                >
                  ↓
                </Button>
                <Button
                  variant="quiet"
                  className="min-w-11 px-2"
                  aria-label={t("mission.remove", { name: s.character })}
                  onPress={() => setDraft(remove(draft, s.id))}
                >
                  ✕
                </Button>
              </li>
            ))}
          </ol>
        )}
        <div className="flex flex-wrap gap-2">
          <Button
            variant="secondary"
            isDisabled={busy || !draft.name.trim() || draft.steps.length === 0}
            onPress={() => void save(false)}
          >
            {t("mission.saveDraft")}
          </Button>
          <Button
            isDisabled={busy || !draft.name.trim() || draft.steps.length === 0}
            onPress={() => void save(true)}
          >
            {t("mission.saveRun")}
          </Button>
          <Button variant="quiet" onPress={() => setDraft(null)}>
            {t("mission.cancel")}
          </Button>
        </div>
      </section>
    );
  }
  return (
    <section className="flex flex-col gap-3">
      {env.act && state.can_edit ? (
        <Button variant="secondary" onPress={() => setDraft(emptyDraft())}>
          {t("mission.new")}
        </Button>
      ) : null}
      {state.quests.length === 0 ? (
        <p className="m-0 text-muted">{t("mission.none")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          {state.quests.map((q) => (
            <li key={q.id} className="rounded-field border border-line bg-paper p-2">
              <p className="m-0 font-semibold">{q.name}</p>
              <p className="m-0 text-sm text-muted">
                {t(`mission.state.${q.state}`, { defaultValue: q.state })} ·{" "}
                {t("mission.steps", { count: q.steps.length })}
              </p>
              <ol className="m-0 mt-1 list-none p-0 text-sm">
                {q.steps.map((s, i) => (
                  <li
                    key={s.id}
                    data-current={s.current ? "true" : undefined}
                    className={s.current ? "font-semibold text-accent" : ""}
                  >
                    {i + 1}. {s.title}
                    {s.waiting ? <span className="text-muted"> — {s.waiting}</span> : null}
                  </li>
                ))}
              </ol>
              {q.editable && state.can_edit ? (
                <Button
                  variant="quiet"
                  className="mt-1"
                  onPress={() => setDraft(fromQuest(q, state))}
                >
                  {t("mission.adjustButton")}
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Rituals({ state }: { state: CanvasState }) {
  const { t, i18n } = useTranslation("canvas");
  const env = useCanvasEnv();
  const qc = useQueryClient();
  const q = useQuery(ritualsQuery(state.workspace, state.table));
  const edit = useMutation({
    mutationFn: ({ id, enabled, content }: { id: string; enabled?: boolean; content?: string }) =>
      env.act.editRitual(id, { enabled, content }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: ["canvas", state.workspace, state.table, "rituals"] }),
    onError: env.fail,
  });
  if (q.isPending) return <p className="m-0 text-muted">{t("loading")}</p>;
  const list = q.data?.rituals ?? [];
  if (list.length === 0) return <p className="m-0 text-muted">{t("ritual.none")}</p>;
  return (
    <ul className="m-0 flex list-none flex-col gap-2 p-0">
      {list.map((r) => (
        <RitualRow
          key={r.id}
          r={r}
          canEdit={state.can_edit}
          busy={edit.isPending}
          when={r.next_run ? formatDateTime(r.next_run, i18n.language, env.timeZone) : ""}
          onChange={(p) => edit.mutate({ id: r.id, ...p })}
        />
      ))}
    </ul>
  );
}

function RitualRow({
  r,
  canEdit,
  busy,
  when,
  onChange,
}: {
  r: RitualInfo;
  canEdit: boolean;
  busy: boolean;
  when: string;
  onChange: (p: { enabled?: boolean; content?: string }) => void;
}) {
  const { t } = useTranslation("canvas");
  const [text, setText] = useState<string | null>(null);
  return (
    <li className="rounded-field border border-line bg-paper p-2">
      <p className="m-0 font-semibold">{r.name}</p>
      <p className="m-0 text-sm text-muted">
        {r.schedule} · {r.enabled ? t("ritual.on") : t("ritual.off")}
        {when ? ` · ${t("ritual.next", { when })}` : ""}
      </p>
      <p className="m-0 text-sm">{t("ritual.target", { target: r.target || "—" })}</p>
      {text === null ? (
        <p className="m-0 whitespace-pre-wrap text-sm">{r.content}</p>
      ) : (
        <Field label={t("ritual.content")} value={text} onChange={setText} />
      )}
      {canEdit ? (
        <div className="mt-1 flex flex-wrap gap-2">
          <Button
            variant="secondary"
            isDisabled={busy}
            onPress={() => onChange({ enabled: !r.enabled })}
          >
            {r.enabled ? t("ritual.disable") : t("ritual.enable")}
          </Button>
          {text === null ? (
            <Button variant="quiet" onPress={() => setText(r.content)}>
              {t("ritual.edit")}
            </Button>
          ) : (
            <>
              <Button
                isDisabled={busy || !text.trim()}
                onPress={() => {
                  onChange({ content: text });
                  setText(null);
                }}
              >
                {t("ritual.save")}
              </Button>
              <Button variant="quiet" onPress={() => setText(null)}>
                {t("ritual.cancel")}
              </Button>
            </>
          )}
        </div>
      ) : null}
    </li>
  );
}

function Grimoires({ state }: { state: CanvasState }) {
  const { t, i18n } = useTranslation("canvas");
  const env = useCanvasEnv();
  const q = useQuery(grimoiresQuery);
  const [plan, setPlan] = useState<GrimoirePlan | null>(null);
  const [choices, setChoices] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  const run = async (name: string, confirm: boolean) => {
    setBusy(true);
    try {
      const p = await env.act.restoreGrimoire({ name, confirm, choices });
      setPlan(p);
      if (p.done) env.changed();
    } catch (err) {
      env.fail(err);
    } finally {
      setBusy(false);
    }
  };

  if (plan && !plan.done) {
    const ready = plan.choose.every((c) => choices[c]);
    return (
      <section aria-label={t("grimoire.plan", { name: plan.name })} className="flex flex-col gap-3">
        <h2 className="m-0 text-xl">{t("grimoire.plan", { name: plan.name })}</h2>
        {plan.renamed.length > 0 ? (
          <div>
            <p className="m-0 font-semibold">{t("grimoire.renamed")}</p>
            <ul className="m-0 pl-5 text-sm">
              {plan.renamed.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          </div>
        ) : null}
        {plan.choose.map((c) => (
          <label key={c} className="flex flex-col gap-1 text-sm">
            <span className="font-semibold">{t("grimoire.missingClass", { name: c })}</span>
            <select
              className="min-h-11 rounded-field border border-line bg-paper px-2"
              value={choices[c] ?? ""}
              onChange={(e) => setChoices({ ...choices, [c]: e.target.value })}
            >
              <option value="">{t("grimoire.choose")}</option>
              <option value="copy">{t("grimoire.copy")}</option>
              <option value="cancel">{t("grimoire.cancel")}</option>
            </select>
          </label>
        ))}
        <div className="flex flex-wrap gap-2">
          <Button isDisabled={busy || !ready} onPress={() => void run(plan.name, true)}>
            {t("grimoire.confirm")}
          </Button>
          <Button variant="quiet" onPress={() => setPlan(null)}>
            {t("grimoire.back")}
          </Button>
        </div>
      </section>
    );
  }
  return (
    <section className="flex flex-col gap-3">
      {plan?.done ? (
        <p role="status" className="m-0 rounded-field bg-accent-wash px-3 py-2">
          {plan.failed && plan.failed.length > 0
            ? t("grimoire.doneWithFailures", { list: plan.failed.join(", ") })
            : t("grimoire.done", { name: plan.name })}
        </p>
      ) : null}
      {q.isPending ? <p className="m-0 text-muted">{t("loading")}</p> : null}
      {q.data && q.data.grimoires.length === 0 ? (
        <p className="m-0 text-muted">{t("grimoire.none")}</p>
      ) : null}
      <ul className="m-0 flex list-none flex-col gap-2 p-0">
        {(q.data?.grimoires ?? []).map((g) => (
          <li key={g.name} className="rounded-field border border-line bg-paper p-2">
            <p className="m-0 font-semibold">{g.name}</p>
            <p className="m-0 text-sm text-muted">
              {t("grimoire.origin", {
                workspace: g.workspace,
                table: g.table,
                author: g.author,
                when: formatDateTime(g.saved_at, i18n.language, env.timeZone),
              })}
            </p>
            <p className="m-0 text-sm text-muted">
              {t("grimoire.contents", {
                characters: g.characters,
                scrolls: g.scrolls,
                tomes: g.tomes,
                quests: g.quests,
              })}
            </p>
            {state.can_edit ? (
              <Button
                variant="secondary"
                className="mt-1"
                isDisabled={busy}
                onPress={() => void run(g.name, false)}
              >
                {t("grimoire.restore")}
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
