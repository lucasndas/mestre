import { useTranslation } from "react-i18next";
import { useChatControl } from "../../app/chatControl";
import { useCan } from "../../app/session";
import { Button } from "../../components/ui";
import { useCurrentWorkspace } from "../../workspace/current";

/** The big button of the Home: opens the chat on the current campaign; off, with the reason, when it cannot. */
export function TalkButton() {
  const { t } = useTranslation("home");
  const chat = useChatControl();
  const { slug, ready, list, canCreate } = useCurrentWorkspace();
  const canTalk = useCan("talk_to_grand_master");
  let reason = "";
  if (!canTalk) reason = t("talk.noLevel");
  else if (ready && list.length === 0)
    reason = canCreate ? t("talk.noWorkspace") : t("talk.noAccess");
  const off = reason !== "" || !ready;
  return (
    <div className="mt-6">
      <Button
        className="min-h-14 px-6 font-display text-xl"
        isDisabled={off}
        aria-describedby="home-talk-about"
        onPress={chat.show}
      >
        {t("talk.button")}
      </Button>
      <p id="home-talk-about" className="mt-2 text-muted">
        {off ? reason : t("talk.about", { workspace: slug })}
      </p>
    </div>
  );
}
