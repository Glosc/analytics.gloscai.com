import { region } from "@features/env";
import { RegionFlag } from "@components/RegionFlag";
import { useT } from "@i18n/index";

export function DataResidency() {
  const t = useT();

  if (!region) return null;

  return (
    <div className="justify-center text-xs tracking-normal text-muted-foreground flex items-center p-2">
      <div className="p-1">
        <RegionFlag />
      </div>
      <p>{t("{region} Data Residency", { region: region.toUpperCase() })}</p>
    </div>
  );
}
