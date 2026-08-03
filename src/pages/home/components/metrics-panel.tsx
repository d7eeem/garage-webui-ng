import { useEffect, useState } from "react";
import { Card, Loading } from "react-daisyui";
import {
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { readableBytes } from "@/lib/utils";
import { useClusterMetrics } from "../hooks";
import { ClusterMetrics } from "../types";

// One in-memory history point: the poll timestamp plus every curated metric
// value at that poll. This is the chart source — it lives only in browser
// memory for as long as the page is open (the backend is stateless and keeps
// no history of its own), and resets on unmount by design.
type HistoryEntry = { t: number } & ClusterMetrics;

const MAX_HISTORY = 30;

// Friendlier labels for the backend's curated metric families (see
// curatedMetrics in backend/router/metrics.go). If that list ever changes,
// an unrecognized key still renders with a mechanically formatted label
// instead of being dropped.
const METRIC_LABELS: Record<string, string> = {
  api_s3_request_counter: "S3 Requests",
  api_s3_error_counter: "S3 Errors",
  block_bytes_read: "Block Bytes Read",
  block_bytes_written: "Block Bytes Written",
};

const isBytesMetric = (key: string) => key.startsWith("block_bytes_");

const formatMetricLabel = (key: string) =>
  METRIC_LABELS[key] ??
  key.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());

const formatMetricValue = (key: string, value: number) =>
  isBytesMetric(key) ? readableBytes(value) : value.toLocaleString();

const MetricsPanel = () => {
  const { data, error, isLoading } = useClusterMetrics();
  const [history, setHistory] = useState<HistoryEntry[]>([]);

  useEffect(() => {
    if (!data) return;

    setHistory((prev) => {
      const next = [...prev, { t: Date.now(), ...data }];
      return next.length > MAX_HISTORY
        ? next.slice(next.length - MAX_HISTORY)
        : next;
    });
  }, [data]);

  const metricKeys = Object.keys(data ?? {});

  return (
    <Card className="card-body mt-4 md:mt-8">
      <Card.Title>Cluster Metrics (live)</Card.Title>

      {error != null && (
        <p className="text-sm text-base-content/60">
          Metrics unavailable — set a metrics token in garage.toml (see the{" "}
          <code>admin</code> section of Garage&apos;s config).
        </p>
      )}

      {error == null && isLoading && (
        <div className="py-4 flex justify-center">
          <Loading size="sm" />
        </div>
      )}

      {error == null && !isLoading && data != null && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 md:gap-6 mt-2">
          {metricKeys.map((key) => (
            <div key={key} className="bg-base-100 rounded-box p-4">
              <p className="text-sm truncate">{formatMetricLabel(key)}</p>
              <p className="text-2xl font-bold mt-1 truncate">
                {formatMetricValue(key, data[key])}
              </p>

              <div className="h-12 mt-2 text-primary">
                <ResponsiveContainer width="100%" height="100%">
                  <LineChart data={history}>
                    <XAxis dataKey="t" hide />
                    <YAxis hide domain={["auto", "auto"]} />
                    <Tooltip />
                    <Line
                      type="monotone"
                      dataKey={key}
                      stroke="currentColor"
                      strokeWidth={2}
                      dot={false}
                      isAnimationActive={false}
                    />
                  </LineChart>
                </ResponsiveContainer>
              </div>
            </div>
          ))}
        </div>
      )}
    </Card>
  );
};

export default MetricsPanel;
