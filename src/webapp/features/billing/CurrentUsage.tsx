import { PingSignal } from "@components/PingSignal";
import { twMerge } from "tailwind-merge";
import { formatMonthName } from "@fns/format-date";
import { useT } from "@i18n/index";

type Props = {
  month: number;
  year: number;
  usage: number;
  quota: number;
  state: "OK" | "OVERUSE";
};

const colors = {
  OK: "bg-primary",
  OVERUSE: "bg-destructive",
};

export function CurrentUsage(props: Props) {
  const t = useT();
  const percentage = (props.usage / props.quota) * 100;

  return (
    <div className="flex flex-col h-full justify-between">
      <div className="flex items-center mb-1 justify-between">
        <span>{t("Usage")}</span>
        <span className="text-sm text-muted-foreground">
          {formatMonthName(props.month)} / {props.year}
        </span>
      </div>
      <div className="space-y-1">
        <div className="text-sm flex items-center space-x-1">
          <span>{props.usage.toLocaleString()}</span>
          <span className="text-muted-foreground">
            {t("/ {quota} events ({percentage}%)", {
              quota: props.quota.toLocaleString(),
              percentage: percentage.toFixed(2),
            })}
          </span>
          {props.state === "OVERUSE" && <PingSignal color="warning" size="xs" />}
        </div>
        <div className="overflow-hidden rounded bg-accent">
          <div className={twMerge("h-2 rounded", colors[props.state])} style={{ width: `${percentage}%` }} />
        </div>
      </div>
    </div>
  );
}
