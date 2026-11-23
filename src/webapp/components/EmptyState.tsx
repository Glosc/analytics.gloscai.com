import { useT } from "@i18n/index";

export function EmptyState() {
  const t = useT();

  return (
    <div className="bg-muted rounded w-full h-full flex flex-col space-y-1 items-center justify-center">
      <p className="text-lg">{t("No Data")}</p>
      <p className="text-sm text-muted-foreground">{t("There's no data available for your selection.")}</p>
    </div>
  );
}
