import { ReactNode, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  AlertCircle,
  Ban,
  Check,
  ChevronDown,
  ChevronUp,
  Clock,
  Copy,
  X,
} from "lucide-react";

import Button from "@/components/ui/button";
import { cn, copyToClipboard } from "@/lib/utils";
import { Z_LAYERS } from "@/lib/z-layers";
import { useConfig } from "@/hooks/useConfig";
import { useBuckets } from "@/pages/buckets/hooks";
import type { Bucket } from "@/pages/buckets/types";
import { getPublicAccess } from "@/lib/website";
import type { Config } from "@/types/garage";
import uploadQueue, {
  useUploadQueue,
} from "@/pages/buckets/manage/browse/upload-queue";
import type {
  UploadItem,
  UploadStatus,
} from "@/pages/buckets/manage/browse/types";

/** The prefix an item was uploaded to, derived from its full key and display
 * name (key = prefix + name) rather than stored separately. */
const itemPrefix = (item: UploadItem): string =>
  item.key.endsWith(item.name)
    ? item.key.slice(0, item.key.length - item.name.length)
    : "";

/**
 * Fades + slides `children` in on mount, using only core Tailwind opacity /
 * translate utilities behind `motion-safe:` (no animation plugin is a
 * dependency here, so `animate-in`-style utility classes are not available).
 * Used for the card's own entrance and for the row list's expand.
 */
const FadeIn = ({ children }: { children: ReactNode }) => {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    const raf = requestAnimationFrame(() => setVisible(true));
    return () => cancelAnimationFrame(raf);
  }, []);

  return (
    <div
      className={cn(
        "motion-safe:transition-all motion-safe:duration-200 motion-safe:ease-out",
        visible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-2"
      )}
    >
      {children}
    </div>
  );
};

/**
 * Persistent floating card reporting the state of `upload-queue.ts`'s
 * (already-global) upload queue. Mounted once in `main-layout.tsx`, so it
 * survives route changes — uploads themselves already did; only their UI was
 * previously scoped to the browse tab that started them.
 *
 * Renders nothing when the queue is empty; otherwise sits fixed above the
 * page content (but below toasts, and below a native <dialog>'s top layer —
 * see src/lib/z-layers.ts).
 */
const UploadCard = () => {
  const { items, collapsed, completed } = useUploadQueue();
  const queryClient = useQueryClient();
  const { data: buckets } = useBuckets();
  const { data: config } = useConfig();

  // Invalidates the *uploaded-to* bucket's listing, not whichever bucket the
  // user happens to be viewing — the queue can hold items from several
  // buckets, so `completed.bucket` (set per-success in upload-queue.ts) is
  // the only reliable source for this.
  const lastInvalidatedSeq = useRef(0);
  useEffect(() => {
    if (!completed || completed.seq === lastInvalidatedSeq.current) return;
    lastInvalidatedSeq.current = completed.seq;
    queryClient.invalidateQueries({ queryKey: ["browse", completed.bucket] });
  }, [completed, queryClient]);

  // aria-live region: announces *transitions* only (a completion count going
  // up, or a file newly failing) — never percentages, which would spam a
  // screen reader every progress tick.
  const [liveMessage, setLiveMessage] = useState("");
  const prevRef = useRef<{
    statuses: Map<string, UploadStatus>;
    done: number;
  }>({ statuses: new Map(), done: 0 });
  useEffect(() => {
    const prevStatuses = prevRef.current.statuses;
    let failedName: string | null = null;
    for (const item of items) {
      if (item.status === "error" && prevStatuses.get(item.id) !== "error") {
        failedName = item.name;
        break;
      }
    }

    const doneCount = items.filter((item) => item.status === "done").length;

    if (failedName) {
      setLiveMessage(`${failedName} failed`);
    } else if (doneCount > prevRef.current.done) {
      setLiveMessage(`${doneCount} of ${items.length} uploads complete`);
    }

    prevRef.current = {
      statuses: new Map(items.map((item) => [item.id, item.status])),
      done: doneCount,
    };
  }, [items]);

  if (items.length === 0) {
    return null;
  }

  const doneCount = items.filter((item) => item.status === "done").length;
  const activeItems = items.filter(
    (item) => item.status === "queued" || item.status === "uploading"
  );
  const activeCount = activeItems.length;

  // Misleading aggregates are forbidden: an aggregate percentage is only
  // shown when every active item's size is known and non-zero.
  const canAggregate =
    activeItems.length > 0 && activeItems.every((item) => item.size > 0);
  const aggregatePct = canAggregate
    ? Math.round(
        (activeItems.reduce((sum, item) => sum + item.loaded, 0) /
          activeItems.reduce((sum, item) => sum + item.size, 0)) *
          100
      )
    : null;

  const distinctBuckets = new Set(items.map((item) => item.bucket)).size;
  const showDestination = distinctBuckets > 1;

  return (
    <FadeIn>
      <div
        className={cn(
          "fixed bottom-4 inset-x-4 sm:inset-x-auto sm:right-4 sm:w-[min(92vw,26rem)] w-auto",
          "bg-base-200 border border-base-300 rounded-lg shadow-lg overflow-hidden"
        )}
        style={{ zIndex: Z_LAYERS.uploadCard }}
      >
        <div
          className="flex flex-row items-center gap-2 px-3 py-2 cursor-pointer select-none"
          onClick={() => uploadQueue.toggleCollapsed()}
        >
          <span className="flex-1 text-sm font-medium truncate">
            {collapsed ? (
              <>
                Uploading {activeCount} of {items.length}
                {aggregatePct != null && (
                  <span className="opacity-70"> · {aggregatePct}%</span>
                )}
              </>
            ) : (
              <>
                Uploads{" "}
                <span className="opacity-70">
                  {doneCount} / {items.length}
                </span>
              </>
            )}
          </span>

          <Button
            icon={collapsed ? ChevronUp : ChevronDown}
            size="sm"
            color="ghost"
            shape="circle"
            aria-expanded={!collapsed}
            aria-label={
              collapsed ? "Expand upload list" : "Collapse upload list"
            }
            onClick={(e) => {
              e.stopPropagation();
              uploadQueue.toggleCollapsed();
            }}
          />
          <Button
            icon={X}
            size="sm"
            color="ghost"
            shape="circle"
            aria-label="Clear completed uploads"
            onClick={(e) => {
              e.stopPropagation();
              uploadQueue.clearFinished();
            }}
          />
        </div>

        {!collapsed && (
          <FadeIn>
            <ul className="flex flex-col gap-1 max-h-[45vh] overflow-y-auto px-3 pb-3">
              {items.map((item) => (
                <UploadRow
                  key={item.id}
                  item={item}
                  showDestination={showDestination}
                  buckets={buckets}
                  config={config}
                />
              ))}
            </ul>
          </FadeIn>
        )}

        <div aria-live="polite" className="sr-only">
          {liveMessage}
        </div>
      </div>
    </FadeIn>
  );
};

type UploadRowProps = {
  item: UploadItem;
  showDestination: boolean;
  buckets: Bucket[] | undefined;
  config: Config | undefined;
};

const UploadRow = ({
  item,
  showDestination,
  buckets,
  config,
}: UploadRowProps) => {
  const pct = item.size > 0 ? Math.round((item.loaded / item.size) * 100) : 0;

  return (
    <li className="flex flex-col gap-0.5 text-sm py-1 border-b border-base-300 last:border-b-0">
      <div className="flex flex-row items-center gap-2">
        <span className="truncate max-w-[45%] shrink-0" title={item.key}>
          {item.name}
        </span>

        <div className="flex-1 flex flex-row items-center gap-2 min-w-0">
          {item.status === "queued" && (
            <span className="flex flex-row items-center gap-1 text-xs opacity-70">
              <Clock size={14} /> Waiting
            </span>
          )}

          {item.status === "uploading" && (
            <>
              <progress
                className="progress progress-primary w-full"
                value={pct}
                max={100}
                aria-label={`Upload progress for ${item.name}`}
              />
              <span className="text-xs w-10 text-right shrink-0">{pct}%</span>
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
            <DoneStatus item={item} buckets={buckets} config={config} />
          )}

          {item.status === "error" && (
            <span
              className="flex flex-row items-center gap-1 text-error text-xs truncate min-w-0"
              title={item.error}
            >
              <AlertCircle size={14} className="shrink-0" />
              <span className="truncate">{item.error}</span>
              <Button
                size="sm"
                color="ghost"
                className="shrink-0"
                aria-label={`Retry upload of ${item.name}`}
                onClick={() => uploadQueue.retry(item.id)}
              >
                Retry
              </Button>
            </span>
          )}

          {item.status === "canceled" && (
            <span className="flex flex-row items-center gap-1 text-xs opacity-70">
              <Ban size={14} /> Canceled
            </span>
          )}
        </div>
      </div>

      {showDestination && (
        <span className="text-xs opacity-60 truncate">
          {item.bucket}/{itemPrefix(item)}
        </span>
      )}
    </li>
  );
};

type DoneStatusProps = {
  item: UploadItem;
  buckets: Bucket[] | undefined;
  config: Config | undefined;
};

/**
 * The bucket a `done` item belongs to may not be the one currently mounted
 * (or in view at all) — the card is global. Its public/private state is
 * resolved from the buckets-list cache (already populated by the buckets
 * page via `useBuckets`, src/pages/buckets/hooks.ts) rather than any
 * per-route context, which does not exist here.
 *
 * When the bucket cannot be found in that cache, access is unknown: neither
 * the Copy button nor the Private label is shown. Never guess public.
 */
const DoneStatus = ({ item, buckets, config }: DoneStatusProps) => {
  const bucket = buckets?.find((b) => b.globalAliases.includes(item.bucket));
  const access = bucket
    ? getPublicAccess(bucket.websiteAccess, item.bucket, item.key, config)
    : null;

  return (
    <span className="flex flex-row items-center gap-1 text-success text-xs">
      <Check size={14} /> Uploaded
      {access?.state === "public" && (
        <Button
          icon={Copy}
          size="sm"
          color="ghost"
          shape="circle"
          aria-label={`Copy public URL for ${item.name}`}
          onClick={() => copyToClipboard(access.url)}
        />
      )}
      {access != null && access.state !== "public" && (
        <span className="text-base-content/60">Private</span>
      )}
    </span>
  );
};

export default UploadCard;
