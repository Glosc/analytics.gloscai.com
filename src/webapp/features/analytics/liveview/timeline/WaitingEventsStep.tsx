import { PingSignal } from "@components/PingSignal";
import { useT } from "@i18n/index";

export function WaitingEventsStep() {
  const t = useT();
  return (
    <div className="relative">
      <div className="relative flex items-center space-x-2">
        <div className="p-1.5">
          <PingSignal color="success" />
        </div>
        <span className="text-xs text-muted-foreground">{t("Waiting for more events...")}</span>
      </div>
    </div>
  );
}
