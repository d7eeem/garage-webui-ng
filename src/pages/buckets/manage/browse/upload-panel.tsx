import { useEffect } from "react";
import { AlertCircle, Check, Copy, X } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";

import Button from "@/components/ui/button";
import uploadQueue, {
  getUploadItemPublicUrl,
  useUploadQueue,
} from "./upload-queue";
import { useBucketContext } from "../context";
import { useConfig } from "@/hooks/useConfig";
import { copyToClipboard } from "@/lib/utils";

type Props = {
  bucketName: string;
};

/**
 * In-flow (not floating — see z-layers question this sidesteps) queue panel
 * for the uploads started from the browse tab's Upload button. Reads its
 * state from `upload-queue.ts`'s zustand store; the transport there is
 * XMLHttpRequest, not `fetch`, because `fetch` cannot report upload progress.
 */
const UploadPanel = ({ bucketName }: Props) => {
  const { items, completedCount } = useUploadQueue();
  const queryClient = useQueryClient();
  const { bucket } = useBucketContext();
  const { data: config } = useConfig();

  useEffect(() => {
    if (completedCount > 0) {
      queryClient.invalidateQueries({ queryKey: ["browse", bucketName] });
    }
  }, [completedCount, bucketName, queryClient]);

  if (items.length === 0) {
    return null;
  }

  const activeCount = items.filter(
    (item) => item.status === "queued" || item.status === "uploading"
  ).length;
  const finished = activeCount === 0;

  return (
    <div className="flex flex-col gap-2 mx-2 mb-2 px-3 py-2 bg-base-200 rounded-lg">
      <div className="flex flex-row items-center justify-between gap-2">
        <span className="text-sm font-medium">
          {finished
            ? "Uploads finished"
            : `Uploading ${activeCount} of ${items.length}`}
        </span>

        <div className="flex flex-row items-center gap-1">
          {finished && (
            <Button
              size="sm"
              color="ghost"
              onClick={() => uploadQueue.clearFinished()}
            >
              Clear
            </Button>
          )}
          <Button
            icon={X}
            size="sm"
            color="ghost"
            shape="circle"
            aria-label="Close upload panel"
            onClick={() => uploadQueue.clearFinished()}
          />
        </div>
      </div>

      <ul className="flex flex-col gap-1">
        {items.map((item) => {
          const pct =
            item.size > 0 ? Math.round((item.loaded / item.size) * 100) : 0;

          // Only known accurately for the currently mounted bucket — the
          // panel shows items enqueued from other buckets too, and this
          // component has no way to look up a foreign bucket's
          // websiteAccess. getUploadItemPublicUrl still builds strictly
          // from item.bucket (never the mounted bucketName), so this gate
          // just decides when we trust the websiteAccess value we pass in.
          const publicUrl =
            item.status === "done" && item.bucket === bucketName
              ? getUploadItemPublicUrl(item, bucket.websiteAccess, config)
              : null;

          // Same mounted-bucket gate as publicUrl above: only known
          // accurately for the currently mounted bucket, so a row from
          // another bucket gets neither the button nor this label.
          const isPrivate =
            item.status === "done" &&
            item.bucket === bucketName &&
            publicUrl == null;

          return (
            <li
              key={item.id}
              className="flex flex-row items-center gap-2 text-sm"
            >
              <span
                className="truncate max-w-[40%] shrink-0"
                title={item.key}
              >
                {item.name}
              </span>

              <div className="flex-1 flex flex-row items-center gap-2 min-w-0">
                {item.status === "queued" && (
                  <span className="text-xs opacity-70">Queued</span>
                )}

                {item.status === "uploading" && (
                  <>
                    <progress
                      className="progress progress-primary w-full"
                      value={pct}
                      max={100}
                    />
                    <span className="text-xs w-10 text-right shrink-0">
                      {pct}%
                    </span>
                    <Button
                      icon={X}
                      size="sm"
                      color="ghost"
                      shape="circle"
                      aria-label={`Cancel upload of ${item.name}`}
                      onClick={() => uploadQueue.cancel(item.id)}
                    />
                  </>
                )}

                {item.status === "done" && (
                  <span className="flex flex-row items-center gap-1 text-success text-xs">
                    <Check size={14} /> Done
                    {publicUrl && (
                      <Button
                        icon={Copy}
                        size="sm"
                        color="ghost"
                        shape="circle"
                        aria-label={`Copy public URL for ${item.name}`}
                        onClick={() => copyToClipboard(publicUrl)}
                      />
                    )}
                    {isPrivate && (
                      <span className="text-base-content/60">Private</span>
                    )}
                  </span>
                )}

                {item.status === "error" && (
                  <span
                    className="flex flex-row items-center gap-1 text-error text-xs truncate"
                    title={item.error}
                  >
                    <AlertCircle size={14} className="shrink-0" />
                    <span className="truncate">{item.error}</span>
                  </span>
                )}

                {item.status === "canceled" && (
                  <span className="text-xs opacity-70">Canceled</span>
                )}
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
};

export default UploadPanel;
