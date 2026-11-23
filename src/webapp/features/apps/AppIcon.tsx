import { IconBox } from "@tabler/icons-react";
import { useT } from "@i18n/index";

type Props = {
  iconPath: string;
  className: string;
};

export function AppIcon(props: Props) {
  const t = useT();
  if (props.iconPath) {
    return (
      <img
        src={`/uploads/${props.iconPath}`}
        className={`${props.className} rounded`}
        loading="lazy"
        alt={t("App Icon")}
      />
    );
  }

  return <IconBox strokeWidth={1.75} className={`${props.className} p-0.5 border rounded`} />;
}
