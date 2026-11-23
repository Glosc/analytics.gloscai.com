import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@components/Tooltip";
import { useT } from "@i18n/index";

export function DebugModeBanner() {
  const t = useT();
  return (
    <div className="fixed top-12 z-20 lg:top-0 left-0 lg:left-64 right-0 mb-4">
      <div className="w-full border-t-4 border-t-warning text-center text-xs">
        <div className="inline-block -mt-2 text-warning-foreground px-2 rounded-b-md bg-warning">
          <TooltipProvider>
            <Tooltip delayDuration={0}>
              <TooltipTrigger>
                <span>{t("Debug Data")}</span>
              </TooltipTrigger>
              <TooltipContent>
                <p>{t("The dashboard is showing data from debug mode.")}</p>
                <p>
                  <a
                    target="_blank"
                    className="underline"
                    href="https://aptabase.com/docs/build-modes"
                  >
                    {t("Switch to release mode")}
                  </a>{" "}
                  {t("to see production data.")}
                </p>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </div>
      </div>
    </div>
  );
}
