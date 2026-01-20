// src/hooks/useDomains.ts
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchDomains, fetchZones, fetchStats } from "../services/api";

export const useDomains = (
  initialPage = 1,
  initialPageSize = 10,
  ignoredFilter = "false"
) => {
  const [page, setPage] = useState(initialPage);
  const [pageSize, setPageSize] = useState(initialPageSize);
  const [searchText, setSearchText] = useState("");
  const [selectedStatus, setSelectedStatus] = useState<string | undefined>(undefined);
  const [onlyProxied, setOnlyProxied] = useState(false);
  const [selectedZone, setSelectedZone] = useState<string | null>(null);
  const [sortField, setSortField] = useState<string>("expiry_asc");

  // 獲取 Zones
  const { data: zones } = useQuery({
    queryKey: ["zones"],
    queryFn: fetchZones,
  });

  // 獲取 Domains
  const { data, isLoading, isFetching } = useQuery({
    queryKey: [
      "domains",
      page,
      pageSize,
      sortField,
      searchText,
      selectedStatus,
      onlyProxied,
      selectedZone,
      ignoredFilter,
    ],
    queryFn: () =>
      fetchDomains(
        page,
        pageSize,
        sortField,
        searchText,
        selectedStatus || "",
        onlyProxied ? "true" : "",
        ignoredFilter,
        selectedZone || ""
      ),
    refetchInterval: 10000,
    placeholderData: (prev) => prev, // 保持上一頁資料直到新資料載入，避免閃爍
  });

  // 獲取統計
  const { data: stats, isLoading: statsLoading } = useQuery({
    queryKey: ["stats"],
    queryFn: fetchStats,
    refetchInterval: 10000,
  });

  return {
    // Data
    domainsData: data,
    zones,
    stats,
    loading: isLoading || isFetching,
    statsLoading,
    
    // State
    page, setPage,
    pageSize, setPageSize,
    searchText, setSearchText,
    selectedStatus, setSelectedStatus,
    onlyProxied, setOnlyProxied,
    selectedZone, setSelectedZone,
    sortField, setSortField,
  };
};