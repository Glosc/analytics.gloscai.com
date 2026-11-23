import { useT } from "@i18n/index";
import { getCountryName } from "./countries";

type Props = {
  countryCode: string;
};

export function CountryName(props: Props) {
  const t = useT();

  return <>{getCountryName(props.countryCode) || t("Unknown")}</>;
}
