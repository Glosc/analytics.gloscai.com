import { isManagedCloud, region } from "@features/env";
import { useT } from "@i18n/index";

export function RegionSwitch() {
  const t = useT();

  if (!isManagedCloud) return null;

  const otherRegion = region === "eu" ? "us" : "eu";
  const href = `https://${otherRegion}.aptabase.com`;

  return (
    <div className="justify-center text-xs tracking-normal text-muted-foreground flex items-center p-2">
      {t("Looking for {region} hosting?", { region: otherRegion.toUpperCase() })}
      <a href={href} className="text-primary ml-1">
        {t("Switch →")}
      </a>
    </div>
  );
}
