import { Button } from "@components/Button";
import { EmptyState } from "@components/EmptyState";
import { ErrorState } from "@components/ErrorState";
import { LoadingState } from "@components/LoadingState";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@components/Select";
import { useApps } from "@features/apps";
import {
  IconAlertTriangle,
  IconChevronLeft,
  IconChevronRight,
  IconClock,
  IconDeviceDesktop,
  IconFilter,
} from "@tabler/icons-react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useAtomValue } from "jotai";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { dateFilterValuesAtom } from "../../../atoms/date-atoms";
import { formatDate, formatTime } from "@fns/format-date";
import { useT } from "@i18n/index";
import { OSIcon } from "../dashboard/icons/os";
import { DateFilterContainer } from "../date-filters/DateFilterContainer";
import { OsFilterDropdown } from "../sessions/filters/OsFilterDropdown";
import { ErrorDetailModal } from "./ErrorDetailModal";
import { ErrorTypeFilterDropdown } from "./ErrorTypeFilterDropdown";

interface ErrorItem {
  errorId: string;
  appId: string;
  timestamp: string;
  errorMessage: string;
  errorType: string;
  stackTrace: string;
  platform: string;
  osName: string;
  osVersion: string;
  appVersion: string;
  sdkVersion: string;
  sessionId: string;
  severity: string;
  kind: string;
}

interface ErrorsResponse {
  errors: ErrorItem[];
  pagination: {
    offset: number;
    limit: number;
    total: number;
  };
}

async function fetchErrors(
  appId: string,
  buildMode: string,
  offset: number,
  limit: number,
  startDate?: string,
  endDate?: string,
  osName?: string,
  errorType?: string,
  severity?: string,
): Promise<ErrorsResponse> {
  const params = new URLSearchParams({
    buildMode,
    offset: offset.toString(),
    limit: limit.toString(),
  });

  if (startDate) params.set("startDate", startDate);
  if (endDate) params.set("endDate", endDate);
  if (osName && osName !== "all") params.set("osName", osName);
  if (errorType && errorType !== "all") params.set("errorType", errorType);
  if (severity && severity !== "all") params.set("severity", severity);

  const response = await fetch(`/api/v0/apps/${appId}/errors?${params}`, {
    credentials: "include",
  });

  if (!response.ok) {
    throw new Error("Failed to fetch errors");
  }

  return response.json();
}

function SeverityBadge({ severity }: { severity: string }) {
  const t = useT();

  if (!severity) {
    return <span className="text-muted-foreground text-xs">—</span>;
  }

  const styles: Record<string, string> = {
    fatal: "bg-red-500/15 text-red-600 dark:text-red-400",
    error: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  };
  const cls = styles[severity] ?? "bg-muted text-muted-foreground";

  return (
    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium capitalize ${cls}`}>
      {t(severity === "fatal" ? "Fatal" : "Error")}
    </span>
  );
}

interface ErrorsListProps {
  appId: string;
}

export function ErrorsList({ appId }: ErrorsListProps) {
  const t = useT();
  const { buildMode } = useApps();
  const [offset, setOffset] = useState(0);
  const limit = 50;
  const [searchParams, setSearchParams] = useSearchParams();
  const dateFilters = useAtomValue(dateFilterValuesAtom);
  const [selectedErrorId, setSelectedErrorId] = useState<string | null>(null);

  // Get filter values from URL params
  const osName = searchParams.get("osName") || "all";
  const errorType = searchParams.get("errorType") || "all";
  const severity = searchParams.get("severity") || "all";

  const { data, isLoading, isError, refetch, isPlaceholderData } = useQuery({
    queryKey: [
      "errors",
      appId,
      buildMode,
      offset,
      limit,
      dateFilters.startDateIso,
      dateFilters.endDateIso,
      osName,
      errorType,
      severity,
    ],
    queryFn: () =>
      fetchErrors(
        appId,
        buildMode,
        offset,
        limit,
        dateFilters.startDateIso,
        dateFilters.endDateIso,
        osName !== "all" ? osName : undefined,
        errorType !== "all" ? errorType : undefined,
        severity !== "all" ? severity : undefined,
      ),
    placeholderData: keepPreviousData,
  });

  const handleOsNameChange = (value: string) => {
    const newParams = new URLSearchParams(searchParams);
    if (value === "all" || !value) {
      newParams.delete("osName");
    } else {
      newParams.set("osName", value);
    }
    setSearchParams(newParams);
    setOffset(0); // Reset to first page when filters change
  };

  const handleErrorTypeChange = (value: string) => {
    const newParams = new URLSearchParams(searchParams);
    if (value === "all") {
      newParams.delete("errorType");
    } else {
      newParams.set("errorType", value);
    }
    setSearchParams(newParams);
    setOffset(0); // Reset to first page when filters change
  };

  const handleSeverityChange = (value: string) => {
    const newParams = new URLSearchParams(searchParams);
    if (value === "all") {
      newParams.delete("severity");
    } else {
      newParams.set("severity", value);
    }
    setSearchParams(newParams);
    setOffset(0); // Reset to first page when filters change
  };

  const handlePreviousPage = () => {
    if (offset - limit >= 0) {
      setOffset(offset - limit);
    }
  };

  const handleNextPage = () => {
    if (data && offset + limit < data.pagination.total) {
      setOffset(offset + limit);
    }
  };

  const hasNextPage = data && offset + limit < data.pagination.total;
  const hasPreviousPage = offset > 0;

  if (isLoading) {
    return <LoadingState size="lg" />;
  }

  if (isError) {
    return <ErrorState refetch={refetch} />;
  }

  if (!data) {
    return <EmptyState />;
  }

  return (
    <div className="mt-6">
      {/* Filters */}
      <div className="mb-6 flex flex-wrap items-center gap-4">
        <div className="flex items-center gap-2">
          <IconFilter className="h-5 w-5 text-muted-foreground" />
          <span className="text-sm font-medium">{t("Filters:")}</span>
        </div>

        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">{t("Date Range:")}</span>
          <DateFilterContainer />
        </div>

        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">{t("OS:")}</span>
          <OsFilterDropdown appId={appId} onValueChange={(osName) => handleOsNameChange(osName ?? "all")} />
        </div>

        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">{t("Error Type:")}</span>
          <ErrorTypeFilterDropdown appId={appId} value={errorType} onValueChange={handleErrorTypeChange} />
        </div>

        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">{t("Severity:")}</span>
          <Select value={severity} onValueChange={handleSeverityChange}>
            <SelectTrigger className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("All")}</SelectItem>
              <SelectItem value="fatal">{t("Fatal")}</SelectItem>
              <SelectItem value="error">{t("Error")}</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      {data.errors.length === 0 && <EmptyState />}

      {data.errors.length > 0 && (
        <div className="flow-root">
          <div className="overflow-x-auto">
            <div className="inline-block min-w-full py-2 align-middle">
              <table className="min-w-full divide-y divide-gray-300">
                <thead>
                  <tr>
                    <th className="py-3.5 pl-4 pr-3 text-left text-sm font-semibold">
                      <div className="flex items-center gap-2">
                        <IconClock className="text-muted-foreground h-5 w-5" />
                        {t("Timestamp")}
                      </div>
                    </th>
                    <th className="px-3 py-3.5 text-left text-sm font-semibold">
                      <div className="flex items-center gap-2">
                        <IconAlertTriangle className="text-muted-foreground h-5 w-5" />
                        {t("Error Type")}
                      </div>
                    </th>
                    <th className="px-3 py-3.5 text-left text-sm font-semibold">{t("Severity")}</th>
                    <th className="px-3 py-3.5 text-left text-sm font-semibold">{t("Message")}</th>
                    <th className="px-3 py-3.5 text-left text-sm font-semibold">
                      <div className="flex items-center gap-2">
                        <IconDeviceDesktop className="text-muted-foreground h-5 w-5" />
                        {t("OS")}
                      </div>
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-600">
                  {data.errors.map((error) => (
                    <tr
                      key={error.errorId}
                      className="hover:bg-accent cursor-pointer"
                      onClick={() => setSelectedErrorId(error.errorId)}
                    >
                      <td className="whitespace-nowrap py-4 pl-4 pr-3 text-sm">
                        {formatDate(error.timestamp)} {formatTime(error.timestamp)}
                      </td>
                      <td className="whitespace-nowrap px-3 py-4 text-sm">{error.errorType}</td>
                      <td className="whitespace-nowrap px-3 py-4 text-sm">
                        <SeverityBadge severity={error.severity} />
                      </td>
                      <td className="px-3 py-4 text-sm max-w-md truncate">{error.errorMessage}</td>
                      <td className="whitespace-nowrap px-3 py-4 text-sm">
                        <div className="flex items-center gap-2">
                          <span>
                            {error.osName} {error.osVersion}
                          </span>
                          <OSIcon name={error.osName} className="h-5 w-5" />
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}

      {data.pagination.total > 0 && (
      <div className="flex justify-between items-center mt-4">
        <div className="text-sm text-muted-foreground">
          {t("Showing {from} to {to} of {total} errors", {
            from: offset + 1,
            to: Math.min(offset + limit, data.pagination.total),
            total: data.pagination.total,
          })}
        </div>
        <div className="flex gap-2">
          <Button disabled={!hasPreviousPage || isPlaceholderData} variant="ghost" onClick={handlePreviousPage}>
            <IconChevronLeft className="h-4 w-4" />
            {t("Previous")}
          </Button>
          <Button disabled={!hasNextPage || isPlaceholderData} variant="ghost" onClick={handleNextPage}>
            {t("Next")}
            <IconChevronRight className="h-4 w-4" />
          </Button>
        </div>
      </div>
      )}

      <ErrorDetailModal
        appId={appId}
        errorId={selectedErrorId}
        open={!!selectedErrorId}
        onClose={() => setSelectedErrorId(null)}
      />
    </div>
  );
}
