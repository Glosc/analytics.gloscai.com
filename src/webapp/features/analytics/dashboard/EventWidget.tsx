import { useSearchParams } from "react-router-dom";
import { TopNDataContainer } from "./TopNDataContainer";
import { topEvents } from "../query";
import { TopEventProps } from "./TopEventProps";
import { TopNChart } from "./TopNChart";
import { useT } from "@i18n/index";

type Props = {
  appId: string;
};

export function EventWidget(props: Props) {
  const t = useT();
  const [searchParams] = useSearchParams();
  const eventName = searchParams.get("eventName") || "";

  if (eventName) {
    return <TopEventProps appId={props.appId} />;
  }

  return (
    <TopNDataContainer appId={props.appId} queryName="top-events" query={topEvents}>
      {(data) => (
        <TopNChart
          {...data}
          id="events"
          key="events"
          title={t("Events")}
          searchParamKey="eventName"
          pathname={`/${props.appId}/`}
          defaultFormat="absolute"
          valueLabel={t("Count")}
        />
      )}
    </TopNDataContainer>
  );
}
