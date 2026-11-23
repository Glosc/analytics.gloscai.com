import { useI18n } from "@i18n/index";
import { twMerge } from "tailwind-merge";

type Props = {
  className?: string;
};

export function LanguageSwitch(props: Props) {
  const { locale, setLocale, t } = useI18n();

  const optionClassName = (active: boolean) =>
    twMerge(
      "px-2 py-1 rounded text-xs transition-colors",
      active ? "bg-accent text-foreground font-medium" : "text-muted-foreground hover:text-foreground"
    );

  return (
    <div
      role="group"
      className={twMerge("inline-flex items-center rounded border p-0.5", props.className)}
      aria-label={t("Language")}
    >
      <button type="button" onClick={() => setLocale("en")} className={optionClassName(locale === "en")}>
        English
      </button>
      <button type="button" onClick={() => setLocale("zh")} className={optionClassName(locale === "zh")}>
        中文
      </button>
    </div>
  );
}
