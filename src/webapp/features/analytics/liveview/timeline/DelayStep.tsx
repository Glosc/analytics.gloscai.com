import { formatNumber } from "@fns/format-number";
import { IconClock } from "@tabler/icons-react";
import { useT } from "@i18n/index";

type Props = {
  seconds: number;
};

export function DelayStep(props: Props) {
  const t = useT();
  return (
    <div className="relative">
      <span className="absolute left-4 top-4 -ml-px h-full w-0.5 bg-border" />
      <div className="relative flex items-center space-x-2">
        <IconClock className="p-1.5 h-8 w-8 bg-muted border rounded-full" />
        <span className="text-xs">{t("{duration} later", { duration: formatNumber(props.seconds, "duration") })}</span>
      </div>
    </div>
  );
}
