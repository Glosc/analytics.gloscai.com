import { OSIcon } from "./icons/os";
import { useT } from "@i18n/index";

type Props = {
  name: string;
};

export function OS(props: Props) {
  const t = useT();

  return (
    <span className="flex items-center space-x-2">
      <OSIcon name={props.name} className="h-5 w-5" />
      <p>{props.name || t("Unknown")}</p>
    </span>
  );
}
