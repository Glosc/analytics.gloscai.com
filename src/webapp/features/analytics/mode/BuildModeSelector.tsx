import { Popover, PopoverContent, PopoverTrigger } from "@components/Popover";
import { BuildMode, useApps } from "@features/apps";
import { IconBug, IconRocket } from "@tabler/icons-react";
import { twJoin } from "tailwind-merge";
import { useI18n } from "@i18n/index";

const options = [
  { icon: IconRocket, label: "Release", value: "release" as BuildMode },
  { icon: IconBug, label: "Debug", value: "debug" as BuildMode },
];

export function BuildModeSelector() {
  const { t, locale } = useI18n();
  const { buildMode, switchBuildMode } = useApps();

  return (
    <Popover>
      <PopoverTrigger className="relative">
        {buildMode === "release" ? (
          <IconRocket className="h-5 w-5 mb-2" stroke="1" />
        ) : (
          <>
            <div className="absolute rounded-full bg-warning h-1.5 w-1.5 top-0 right-0" />
            <IconBug className="h-5 w-5 mb-2" stroke="1" />
          </>
        )}
      </PopoverTrigger>
      <PopoverContent className="p-2 w-60">
        <div className="text-sm space-y-2 text-center">
          <p className="whitespace-nowrap">{t("What data source do you want see?")}</p>
          <div className="grid grid-cols-2">
            {options.map((option) => (
              <button
                key={option.value}
                value={option.value}
                onClick={() => switchBuildMode(option.value)}
                className={twJoin(
                  "flex cursor-pointer items-center justify-center rounded py-2 focus-ring",
                  option.value === buildMode ? "bg-accent" : "text-muted-foreground"
                )}
              >
                <div className="flex items-center space-x-2">
                  <option.icon className="h-5 w-5" stroke="1.5" />
                  <span>{t(option.label)}</span>
                </div>
              </button>
            ))}
          </div>
          <p className="text-muted-foreground text-xs text-center">
            {t("Your data is separated by")}{" "}
            <a target="_blank" className="underline hover:text-foreground" href="https://aptabase.com/docs/build-modes">
              {t("build mode")}
            </a>
            {locale === "zh" ? "。" : "."}
          </p>
        </div>
      </PopoverContent>
    </Popover>
  );
}
