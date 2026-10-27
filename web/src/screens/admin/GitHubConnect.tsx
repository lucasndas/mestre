import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../../app/session";
import { Alert, Button } from "../../components/ui";
import type { GitHubChallenge, GitHubPoll } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { githubMeQuery, LoadError, PageHeader, StatusStrip } from "./kit";

/**
 * "Conectar GitHub": the device flow of the personal credential. The panel shows the
 * code, asks the engine whether the person authorized at the pace GitHub allows, and
 * shows the error when the account that authorized is not the registered one (the
 * engine has already thrown the token away). No token passes through here.
 */
export function GitHubConnect() {
  const { t } = useTranslation("github");
  const { t: te } = useTranslation("errors");
  const fmt = useFormatDateTime();
  const qc = useQueryClient();
  const { data, error, refetch } = useQuery(githubMeQuery);
  const [challenge, setChallenge] = useState<GitHubChallenge | null>(null);
  const [failure, setFailure] = useState<unknown>(null);
  const [connected, setConnected] = useState(false);
  const [busy, setBusy] = useState(false);

  // one question to the engine for each interval GitHub asks for
  useEffect(() => {
    if (!challenge) return;
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const ask = async () => {
      try {
        const r = await api<GitHubPoll>("POST", "/api/me/github/poll");
        if (!live) return;
        if (r.state === "connected") {
          setChallenge(null);
          setConnected(true);
          await qc.invalidateQueries({ queryKey: ["admin"] });
        } else {
          wait();
        }
      } catch (err) {
        if (!live) return;
        setChallenge(null);
        setFailure(err);
      }
    };
    const wait = () => {
      timer = setTimeout(() => void ask(), challenge.interval_seconds * 1000);
    };
    wait();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [challenge, qc]);

  const begin = async () => {
    setBusy(true);
    setFailure(null);
    setConnected(false);
    try {
      setChallenge(await api<GitHubChallenge>("POST", "/api/me/github/begin"));
    } catch (err) {
      setFailure(err);
    } finally {
      setBusy(false);
    }
  };

  const g = data?.github;
  const stateText = !g
    ? ""
    : g.state === "connected"
      ? t("state.connected", { login: g.login, until: g.expires_at ? fmt(g.expires_at) : "" })
      : g.state === "expired"
        ? t("state.expired", { login: g.login })
        : t("state.none");
  return (
    <section>
      <PageHeader title={t("title")} intro={t("intro")} />
      {data ? (
        <StatusStrip
          tone={g?.state === "connected" ? "ok" : "warn"}
          title={stateText}
          hint={data.login ? t("registered", { login: data.login }) : t("noLogin")}
        >
          {!challenge && data.may && data.login ? (
            <Button onPress={() => void begin()} isDisabled={busy}>
              {g?.state === "" ? t("connect") : t("reconnect")}
            </Button>
          ) : null}
        </StatusStrip>
      ) : null}
      <div className="flex max-w-2xl flex-col gap-4">
        {error ? (
          <LoadError what={t("loadWhat")} error={error} onRetry={() => void refetch()} />
        ) : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {failure ? <Alert>{errorText(te, failure)}</Alert> : null}
        {connected ? <Alert tone="ok">{t("done")}</Alert> : null}
        {data && !data.may ? <p className="text-sm text-muted">{t("mayNot")}</p> : null}
        {challenge ? (
          <div
            className="flex flex-col gap-3 rounded-field border border-accent bg-accent-wash p-5"
            role="status"
            aria-label={t("steps.title")}
          >
            <h2 className="text-xl">{t("steps.title")}</h2>
            <p>{t("steps.type", { uri: challenge.verification_uri, login: data?.login ?? "" })}</p>
            <p className="text-sm text-muted">{t("steps.code")}</p>
            <p
              className="font-mono text-3xl font-semibold tracking-widest select-all"
              data-testid="device-code"
            >
              {challenge.user_code}
            </p>
            <p className="text-sm">{t("steps.valid", { when: fmt(challenge.expires_at) })}</p>
            <p className="text-sm font-semibold">{t("steps.waiting")}</p>
            <div className="flex gap-3">
              <a
                href={challenge.verification_uri}
                target="_blank"
                rel="noreferrer"
                className="inline-flex min-h-11 items-center justify-center rounded-field bg-accent px-4 font-semibold text-accent-ink hover:bg-accent-strong"
              >
                {t("steps.open")}
              </a>
              <Button variant="quiet" onPress={() => setChallenge(null)}>
                {t("steps.cancel")}
              </Button>
            </div>
          </div>
        ) : null}
      </div>
    </section>
  );
}
