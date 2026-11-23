import { api } from "@fns/api";
import { useQuery } from "@tanstack/react-query";
import { useT } from "@i18n/index";

type AppShareInfo = {
  ownerEmail: string;
  hasOwnership: boolean;
  sharedWithCurrentUser: boolean;
  numberOfOtherShares: number;
};

type Props = {
  appId: string;
};

export function AppShareInfo(props: Props) {
  const t = useT();
  const {
    data: requests,
    isSuccess,
    isLoading,
  } = useQuery({
    queryKey: ["appShareInfo"],
    queryFn: () => api.get<AppShareInfo>(`/_apps/${props.appId}/shares/info`),
    refetchOnWindowFocus: true,
    staleTime: 5 * 60 * 1000, // 5 minutes
  });

  if (isLoading || !isSuccess) return null;

  let numberOfOtherSharesText = "";
  if (requests?.numberOfOtherShares > 0) {
    numberOfOtherSharesText =
      requests?.numberOfOtherShares > 1
        ? t("{count} other users", { count: requests.numberOfOtherShares })
        : t("one other user");
  }

  const sharedWithText = requests?.sharedWithCurrentUser
    ? t("You and {others}", { others: numberOfOtherSharesText })
    : numberOfOtherSharesText;

  return (
    <>
      <div className="space-y-2 text-sm">
        <div className="flex items-baseline gap-2">
          <span className="text-xs font-semibold text-muted-foreground tracking-wide whitespace-nowrap">{t("Owner:")}</span>
          <span className="text-foreground">{requests?.ownerEmail}</span>
        </div>

        {!!sharedWithText && (
          <div className="flex items-baseline gap-2">
            <span className="text-xs font-semibold text-muted-foreground tracking-wide whitespace-nowrap">
              {t("Shared With:")}
            </span>
            <span className="text-foreground">{sharedWithText}</span>
          </div>
        )}
      </div>
      {!requests?.hasOwnership && (
        <div className="mt-2 text-xs italic text-muted-foreground">
          {t("This app is shared with you. Your billing plan will not apply to it.")}
        </div>
      )}
    </>
  );
}
