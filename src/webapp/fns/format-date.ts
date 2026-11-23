import { dateLocale, getLocale } from "@i18n/index";
import { hourCycle } from "@features/env";
import { format, parseJSON } from "date-fns";

export function formatDate(value: Date | string | undefined): string {
  if (!value) return "";

  return format(new Date(value), "PP", { locale: dateLocale() });
}

export function formatTime(value: Date | string | undefined): string {
  if (!value) return "";

  return format(new Date(value), "pp", { locale: dateLocale() });
}

export function formatMonthName(month: number): string {
  return format(new Date(2000, month - 1, 1), "MMMM", { locale: dateLocale() });
}

export function formatPeriod(granularity: "hour" | "day" | "month", period: string) {
  try {
    const locale = dateLocale();

    if (granularity === "hour") {
      const hourFormat = getLocale() === "zh" ? "HH:mm" : hourCycle === "h12" ? "haaaaa'm'" : "HH:mm";
      return format(parseJSON(period), hourFormat, { locale });
    }

    const [year, month, day = 1] = period.substring(0, 10).split("-").map((part) => parseInt(part, 10));
    const date = new Date(year, month - 1, day);

    switch (granularity) {
      case "day":
        return format(date, "MMM d", { locale });
      case "month":
        return format(date, "MMM yyyy", { locale });
    }
  } catch (e) {
    return period;
  }
}

export function formatDateWithoutYear(value: Date | string | undefined): string {
  if (!value) return "";

  return format(new Date(value), "MMM d", { locale: dateLocale() });
}
