import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@components/Select";
import { TopNQueryChildrenProps } from "@features/analytics/query";
import { useMemo } from "react";
import { useT } from "@i18n/index";

type Props = {
  value: string | undefined;
  placeholder?: string;
  onValueChange: (value: string | undefined) => void;
  data: TopNQueryChildrenProps;
};

export function FilterDropdownSelect(props: Props) {
  const t = useT();
  if (!props.data.items?.length) {
    return null;
  }

  const options = useMemo(
    () => props.data.items?.filter((i) => !!i.name).sort((a, b) => a.name.localeCompare(b.name)),
    [props.data.items]
  );

  return (
    <Select value={props.value} onValueChange={props.onValueChange}>
      <SelectTrigger
        className="w-48 focus:ring-0 focus:ring-offset-0"
        showClear={!!props.value}
        onClear={() => props.onValueChange("")}
      >
        <SelectValue placeholder={props.placeholder} />
      </SelectTrigger>
      <SelectContent className="max-h-[410px] overflow-y-auto">
        {options.map((option) => (
          <SelectItem key={option.value} value={option.name}>
            {option.name || t("Unknown")}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
