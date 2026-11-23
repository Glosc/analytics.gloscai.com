import { PingSignal } from "@components/PingSignal";
import { useT } from "@i18n/index";

export function WaitingForEventsInfo() {
  const t = useT();

  return (
    <>
      <PingSignal color="success" size="sm" />
      <div>
        <p className="text-center">{t("Waiting for the first event...")}</p>
        <p className="text-center text-muted-foreground">{t("Click to learn more")}</p>
      </div>
    </>
  );
}
