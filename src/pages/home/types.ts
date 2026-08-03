//

export type GetHealthResult = {
  status: string;
  knownNodes: number;
  connectedNodes: number;
  storageNodes: number;
  storageNodesUp: number;
  partitions: number;
  partitionsQuorum: number;
  partitionsAllOk: number;
};

// ClusterMetrics is the curated, JSON-shaped subset of Garage's Prometheus
// /metrics that the backend's GET /metrics returns — one number per curated
// metric family (see backend/router/metrics.go's curatedMetrics).
export type ClusterMetrics = Record<string, number>;
