import { getLocale } from "@i18n/index";
import { FormatDurationOptions, formatDuration, intervalToDuration } from "date-fns";

function durationFormat(): FormatDurationOptions {
  const locale = getLocale();
  const tokens: Record<string, string> =
    locale === "zh"
      ? { xSeconds: "{{count}}秒", xMinutes: "{{count}}分", xHours: "{{count}}小时" }
      : { xSeconds: "{{count}}s", xMinutes: "{{count}}m", xHours: "{{count}}h" };

  return {
    format: ["hours", "minutes", "seconds"],
    locale: {
      formatDistance: (token, count) => tokens[token].replace("{{count}}", count.toString()),
    },
  };
}

function formatLargeNumber(num: number): string {
  if (num >= 1e6) {
    return (num / 1e6).toFixed(2) + "M";
  } else if (num >= 1e3) {
    return (num / 1e3).toFixed(1) + "k";
  }
  return num.toFixed(0);
}

export function formatNumber(value: number | undefined, format?: "number" | "duration"): string {
  if (format === "duration") {
    if (!value || value < 1) return getLocale() === "zh" ? "0秒" : "0s";

    const interval = intervalToDuration({
      start: 0,
      end: (value ?? 0) * 1000,
    });

    return formatDuration(interval, durationFormat());
  }

  return formatLargeNumber(value ?? 0);
}
