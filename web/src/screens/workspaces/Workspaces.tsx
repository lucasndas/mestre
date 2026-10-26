import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCan } from "../../app/session";
import { Term } from "../../components/Term";
import { Alert, Button, Field } from "../../components/ui";
import type {
  CreateWorkspaceRequest,
  SheetOption,
  WorkspaceRow,
  WorkspaceStage,
} from "../../gen/panel";
import { api } from "../../lib/api";
import { useLive, useLiveEvents } from "../../lib/live";
import { Badge, ChoiceGroup, NativeSelect, PageHeader } from "../admin/kit";
import { WORKSPACES_TOPIC, workspacesQuery } from "./queries";
import { refusalText } from "./refusal";
import { Verification } from "./Verification";

type Say = (k: string, o?: object) => string;

const enc = encodeURIComponent;

/** The workspaces (docs/painel/administracao.md): the list, the session zero and what it asks. */
export function Workspaces() {
  const { t } = useTranslation("workspaces");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const live = useLive();
  const { data, error } = useQuery(workspacesQuery);
  useEffect(() => live.subscribe([WORKSPACES_TOPIC]), [live]);
  // the topic says something changed: the list is read again
  useLiveEvents(
    "workspaces:",
    () => void qc.invalidateQueries({ queryKey: workspacesQuery.queryKey }),
  );
  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      <p className="mb-6 max-w-2xl text-sm text-muted">
        <Term name="Workspace" />
      </p>
      <div className="flex max-w-3xl flex-col gap-4">
        {error ? <Alert>{refusalText(t, te, error)}</Alert> : null}
        {!data && !error ? <p className="text-muted">…</p> : null}
        {data?.can_create ? <Create sheets={data.sheets} /> : null}
        {data && data.workspaces.length === 0 ? <p className="text-muted">{t("empty")}</p> : null}
        <ul className="flex flex-col gap-4">
          {data?.workspaces.map((w) => (
            <li key={w.slug}>
              <Workspace w={w} />
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}

function Create({ sheets }: { sheets: SheetOption[] }) {
  const { t } = useTranslation("workspaces");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [repo, setRepo] = useState("");
  const [slug, setSlug] = useState("");
  const [sheet, setSheet] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [made, setMade] = useState<WorkspaceRow | null>(null);
  const [busy, setBusy] = useState(false);
  const chosen = sheet || sheets[0]?.name || "";

  if (!open) {
    return (
      <div>
        {made ? <Alert tone="ok">{t("create.done", { slug: made.slug })}</Alert> : null}
        <Button onPress={() => setOpen(true)}>{t("create.open")}</Button>
      </div>
    );
  }
  return (
    <form
      className="flex flex-col gap-4 rounded-field border border-line bg-surface p-5"
      aria-label={t("create.title")}
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(null);
        const body: CreateWorkspaceRequest = {
          repo: repo.trim(),
          slug: slug.trim() || undefined,
          grand_master_sheet: chosen,
        };
        try {
          const w = await api<WorkspaceRow>("POST", "/api/workspaces", body);
          setMade(w);
          setOpen(false);
          setRepo("");
          setSlug("");
          await qc.invalidateQueries({ queryKey: workspacesQuery.queryKey });
        } catch (err) {
          setError(err);
        } finally {
          setBusy(false);
        }
      }}
    >
      <h2 className="text-xl">{t("create.title")}</h2>
      <p className="text-sm text-muted">{t("create.intro")}</p>
      <Field
        label={t("create.repo")}
        description={t("create.repoHint")}
        value={repo}
        onChange={setRepo}
        isRequired
        autoComplete="off"
      />
      <Field
        label={t("create.slug")}
        description={t("create.slugHint")}
        value={slug}
        onChange={setSlug}
        autoComplete="off"
      />
      {sheets.length === 0 ? (
        <Alert>{t("create.noSheets")}</Alert>
      ) : (
        <NativeSelect
          label={t("create.sheet")}
          value={chosen}
          onChange={setSheet}
          options={sheets.map((s) => ({ value: s.name, label: `${s.name} (${s.agent})` }))}
        />
      )}
      <p className="text-sm text-muted">{t("create.sheetHint")}</p>
      {error ? <Alert>{refusalText(t, te, error)}</Alert> : null}
      <div className="flex gap-2">
        <Button type="submit" isDisabled={busy || repo.trim() === "" || chosen === ""}>
          {t("create.submit")}
        </Button>
        <Button variant="quiet" onPress={() => setOpen(false)}>
          {t("create.cancel")}
        </Button>
      </div>
    </form>
  );
}

const STAGE_TONE: Record<string, "plain" | "ok" | "warn" | "danger"> = {
  done: "ok",
  current: "plain",
  waiting: "warn",
  failed: "danger",
  pending: "plain",
};

function Workspace({ w }: { w: WorkspaceRow }) {
  const { t } = useTranslation("workspaces");
  const say = t as unknown as Say;
  const canConfigure = useCan("configure_workspace") && w.can_act;
  const canPause = useCan("pause") && w.can_act;
  const [showVerification, setShowVerification] = useState(false);
  const question = w.versioning_asked && w.versioning === "";
  return (
    <article className="rounded-field border border-line bg-surface p-5" aria-label={w.slug}>
      <header className="flex flex-wrap items-center gap-2">
        <h2 className="text-xl">{w.repo}</h2>
        <Badge tone={w.state === "active" ? "ok" : w.state === "paused" ? "warn" : "plain"}>
          {say(`state.${w.state}`)}
        </Badge>
        <code className="text-sm text-muted">{w.slug}</code>
      </header>
      <p className="mt-1 text-sm text-muted">
        {w.grand_master_sheet ? t("grandMaster.sheet", { sheet: w.grand_master_sheet }) : null}
        {w.grand_master_sheet ? " · " : null}
        {t("grandMaster.state", { state: say(`grandMasterState.${w.grand_master}`) })}
      </p>
      {w.fetch_error ? (
        <p className="mt-2 text-sm text-danger">{t("fetchError", { error: w.fetch_error })}</p>
      ) : null}
      {w.state === "preparing" ? <Stages w={w} canRetry={canConfigure} /> : null}
      {w.session_zero_pr ? (
        <p className="mt-3 text-sm">
          {t("sessionZeroPR")}{" "}
          {w.session_zero_pr.startsWith("https://github.com/") ? (
            <a
              className="text-accent underline"
              href={w.session_zero_pr}
              rel="noreferrer noopener"
              target="_blank"
            >
              {w.session_zero_pr}
            </a>
          ) : (
            <span>{w.session_zero_pr}</span>
          )}
        </p>
      ) : null}
      {question && canConfigure ? <Versioning slug={w.slug} /> : null}
      {question && !canConfigure ? (
        <p className="mt-3 text-sm text-muted">{t("versioning.waiting")}</p>
      ) : null}
      {!question && w.versioning ? (
        <p className="mt-3 text-sm text-muted">{say(`versioning.answered.${w.versioning}`)}</p>
      ) : null}
      <div className="mt-4 flex flex-wrap items-center gap-2">
        {w.state !== "preparing" && canPause ? <PauseButton w={w} /> : null}
        {w.state !== "preparing" ? (
          <Button
            variant="secondary"
            aria-expanded={showVerification}
            onPress={() => setShowVerification((v) => !v)}
          >
            {t("verification.toggle")}
          </Button>
        ) : null}
      </div>
      {showVerification ? (
        <div className="mt-4 border-t border-line pt-4">
          <h3 className="mb-2 text-lg">{t("verification.title")}</h3>
          <Verification slug={w.slug} canConfirm={canConfigure} />
        </div>
      ) : null}
    </article>
  );
}

export function Stages({ w, canRetry }: { w: WorkspaceRow; canRetry: boolean }) {
  const { t } = useTranslation("workspaces");
  const say = t as unknown as Say;
  return (
    <div className="mt-4">
      <h3 className="text-lg">{t("stages.title")}</h3>
      <ol className="mt-2 flex flex-col gap-2">
        {w.stages.map((s) => (
          <li
            key={s.key}
            aria-current={
              s.state === "current" || s.state === "waiting" || s.state === "failed"
                ? "step"
                : undefined
            }
          >
            <p className="flex flex-wrap items-center gap-2">
              <span className="font-semibold">{say(`stages.names.${s.key}`)}</span>
              <Badge tone={STAGE_TONE[s.state]}>{say(`stages.states.${s.state}`)}</Badge>
            </p>
            <StageDetail w={w} s={s} canRetry={canRetry} />
          </li>
        ))}
      </ol>
    </div>
  );
}

function StageDetail({
  w,
  s,
  canRetry,
}: {
  w: WorkspaceRow;
  s: WorkspaceStage;
  canRetry: boolean;
}) {
  const { t } = useTranslation("workspaces");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  if (s.state === "waiting")
    return <p className="text-sm text-muted">{t("stages.waitingFor", { why: s.waiting ?? "" })}</p>;
  if (s.state !== "failed" || !s.failure) return null;
  return (
    <div className="mt-1 flex flex-col gap-2 text-sm">
      <p className="text-danger">
        {t("stages.failure", { reason: s.failure.reason, count: s.failure.failures })}
      </p>
      {error ? <Alert>{refusalText(t, te, error)}</Alert> : null}
      {canRetry ? (
        <div>
          <Button
            variant="secondary"
            isDisabled={busy}
            aria-label={t("stages.retryNamed", { stage: s.key, slug: w.slug })}
            onPress={async () => {
              setBusy(true);
              setError(null);
              try {
                await api("POST", `/api/workspaces/${enc(w.slug)}/retry`);
                await qc.invalidateQueries({ queryKey: workspacesQuery.queryKey });
              } catch (err) {
                setError(err);
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("stages.retry")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function PauseButton({ w }: { w: WorkspaceRow }) {
  const { t } = useTranslation("workspaces");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const pausing = w.state === "active";
  return (
    <>
      <Button
        variant="secondary"
        isDisabled={busy}
        onPress={async () => {
          setBusy(true);
          setError(null);
          try {
            await api("POST", `/api/workspaces/${enc(w.slug)}/${pausing ? "pause" : "resume"}`);
            await qc.invalidateQueries({ queryKey: workspacesQuery.queryKey });
          } catch (err) {
            setError(err);
          } finally {
            setBusy(false);
          }
        }}
      >
        {pausing ? t("pause") : t("resume")}
      </Button>
      {error ? <Alert>{refusalText(t, te, error)}</Alert> : null}
    </>
  );
}

function Versioning({ slug }: { slug: string }) {
  const { t } = useTranslation("workspaces");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const [answer, setAnswer] = useState("local");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  return (
    <form
      className="mt-4 flex flex-col gap-3 rounded-field border border-accent bg-accent-wash p-4"
      aria-label={t("versioning.title")}
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(null);
        try {
          await api("POST", `/api/workspaces/${enc(slug)}/versioning`, { answer });
          await qc.invalidateQueries({ queryKey: workspacesQuery.queryKey });
        } catch (err) {
          setError(err);
        } finally {
          setBusy(false);
        }
      }}
    >
      <h3 className="text-lg">{t("versioning.title")}</h3>
      <p className="text-sm">{t("versioning.question")}</p>
      <ChoiceGroup
        label={t("versioning.label")}
        value={answer}
        onChange={setAnswer}
        options={[
          { value: "local", label: t("versioning.local"), hint: t("versioning.localHint") },
          {
            value: "versioned",
            label: t("versioning.versioned"),
            hint: t("versioning.versionedHint"),
          },
        ]}
      />
      {error ? <Alert>{refusalText(t, te, error)}</Alert> : null}
      <div>
        <Button type="submit" isDisabled={busy}>
          {t("versioning.submit")}
        </Button>
      </div>
    </form>
  );
}
