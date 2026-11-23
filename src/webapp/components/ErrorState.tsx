import { IconAlertTriangle } from "@tabler/icons-react";
import { QueryObserverResult, RefetchOptions } from "@tanstack/react-query";
import { Button } from "./Button";
import { useT } from "@i18n/index";

type Props = {
  refetch?: (options?: RefetchOptions | undefined) => Promise<QueryObserverResult<any, Error>>;
};

export function ErrorState(props: Props) {
  const t = useT();

  return (
    <div className="w-full h-full text-destructive flex flex-col space-y-1 items-center justify-center">
      <p className="text-lg flex items-center space-x-2">
        <IconAlertTriangle className="h-5 w-5" />
        <span>{t("Oops... Something went wrong.")}</span>
      </p>
      <p className="text-sm text-muted-foreground">
        {t("Please try again. If the problem persists, please contact support.")}
      </p>
      {props.refetch && (
        <Button variant="ghost" onClick={() => props.refetch?.()} type="button">
          {t("Refresh")}
        </Button>
      )}
    </div>
  );
}
