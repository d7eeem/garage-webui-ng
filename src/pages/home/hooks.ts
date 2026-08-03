import api from "@/lib/api";
import { ClusterMetrics, GetHealthResult } from "./types";
import { useQuery } from "@tanstack/react-query";

export const useNodesHealth = () => {
  return useQuery({
    queryKey: ["health"],
    queryFn: () => api.get<GetHealthResult>("/v2/GetClusterHealth"),
  });
};

export const useClusterMetrics = () => {
  return useQuery({
    queryKey: ["cluster-metrics"],
    queryFn: () => api.get<ClusterMetrics>("/metrics"),
    refetchInterval: 5000,
    retry: false,
  });
};
