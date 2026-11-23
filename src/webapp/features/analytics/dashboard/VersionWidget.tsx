import { TopNDataContainer } from "./TopNDataContainer";
import { topAppVersions } from "../query";
import { TopNChart } from "./TopNChart";
import { useT } from "@i18n/index";

type Props = {
  appId: string;
};

export function VersionWidget(props: Props) {
  const t = useT();

  return (
    <TopNDataContainer appId={props.appId} queryName="top-appversions" query={topAppVersions}>
      {(data) => (
        <TopNChart
          {...data}
          id="appversions"
          key="appversions"
          title={t("App Versions")}
          searchParamKey="appVersion"
          defaultFormat="percentage"
          valueLabel={t("Sessions")}
        />
      )}
    </TopNDataContainer>
  );
}
