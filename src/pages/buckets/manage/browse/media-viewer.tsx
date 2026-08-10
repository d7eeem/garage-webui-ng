import { useEffect, useRef, useState } from "react";
import { Alert, Modal } from "react-daisyui";
import mime from "mime/lite";
import { ChevronLeft, ChevronRight, FileWarningIcon } from "lucide-react";
import { API_URL } from "@/lib/api";
import { createDisclosure } from "@/lib/disclosure";
import Button from "@/components/ui/button";

/** How the viewer should render an object, or null if it cannot preview it. */
export type MediaKind = "image" | "video" | "audio" | "pdf" | "text";

// Only the extensions the backend actually serves inline for each type — kept
// in lockstep with inlineSafeContentTypes in backend/router/browse.go. If
// someone adds a type there, add it here too; if someone adds one here only,
// the result is a preview that always falls back to the error state.
const PREVIEWABLE_IMAGE_TYPES = new Set([
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "image/avif",
  "image/bmp",
  "image/x-icon",
  "image/vnd.microsoft.icon",
]);
const PREVIEWABLE_VIDEO_TYPES = new Set(["video/mp4", "video/webm"]);
const PREVIEWABLE_AUDIO_TYPES = new Set(["audio/mpeg", "audio/ogg", "audio/wav"]);

/**
 * Decide how to preview an object, from its file extension.
 *
 * This MIRRORS the backend allowlist in backend/router/browse.go
 * (`inlineSafeContentTypes`) — the backend is the authority and decides from
 * the object's *stored* content type, which we cannot see from the listing.
 * When the two disagree the backend serves application/octet-stream and the
 * media element fails to load, which is why every renderer below has an
 * onError fallback. Keep this list a subset of the Go one.
 *
 * SVG is deliberately absent: it is XML that can carry <script>, the backend
 * refuses to serve it inline, and a preview attempt could only fail.
 */
export function classifyMedia(objectKey: string): MediaKind | null {
  // mime/lite does not know .ico (see backend/router/browse.go's
  // resolveUploadContentType comment) — special-case it directly, mirroring
  // the backend allowlist's image/x-icon / image/vnd.microsoft.icon entries.
  if (objectKey.toLowerCase().endsWith(".ico")) return "image";

  const type = mime.getType(objectKey);
  if (!type) return null;

  // Check SVG before the image/ prefix test below so it can never be
  // classified as previewable.
  if (type === "image/svg+xml") return null;

  if (PREVIEWABLE_IMAGE_TYPES.has(type)) return "image";
  if (PREVIEWABLE_VIDEO_TYPES.has(type)) return "video";
  if (PREVIEWABLE_AUDIO_TYPES.has(type)) return "audio";
  if (type === "application/pdf") return "pdf";
  if (type === "text/plain") return "text";
  return null;
}

export type MediaViewerItem = { objectKey: string; url: string };

export const mediaViewer = createDisclosure<{
  /** Objects in the current view that classifyMedia can render, in display order. */
  items: MediaViewerItem[];
  /** Index into `items` of the object to show first. */
  index: number;
}>();

/** Cap on how much of a text object we read into the DOM. See maintenance notes. */
const TEXT_PREVIEW_LIMIT = 256 * 1024;

const MediaViewer = () => {
  const { isOpen, data, dialogRef } = mediaViewer.use();
  const [index, setIndex] = useState(data?.index ?? 0);
  const [failed, setFailed] = useState(false);

  // Re-seed the current index whenever the caller opens the viewer on a
  // (possibly different) object, including reopening while already mounted.
  useEffect(() => {
    setIndex(data?.index ?? 0);
  }, [data]);

  // Reset the error state whenever the item being shown changes.
  useEffect(() => {
    setFailed(false);
  }, [index, data]);

  const items = data?.items ?? [];
  const item = items[index];
  const kind = item ? classifyMedia(item.objectKey) : null;
  const src = item ? `${API_URL}${item.url}?view=1` : "";

  const onDownload = () => {
    if (!item) return;
    // Matches object-actions.tsx's onDownload.
    window.open(API_URL + item.url + "?dl=1", "_blank");
  };

  const goPrev = () => setIndex((i) => Math.max(0, i - 1));
  const goNext = () => setIndex((i) => Math.min(items.length - 1, i + 1));

  return (
    <Modal ref={dialogRef} open={isOpen} backdrop className="max-w-4xl w-11/12">
      <Modal.Header className="truncate">{item?.objectKey || ""}</Modal.Header>
      <Modal.Body>
        {!item ? null : failed ? (
          <Alert status="warning" icon={<FileWarningIcon />}>
            <div className="flex flex-col gap-2">
              <span>Preview unavailable. You can still download the file.</span>
              <Button onClick={onDownload} className="self-start">
                Download
              </Button>
            </div>
          </Alert>
        ) : (
          <MediaBody
            key={`${item.objectKey}-${index}`}
            item={item}
            kind={kind}
            src={src}
            onError={() => setFailed(true)}
          />
        )}
      </Modal.Body>
      <Modal.Actions className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {items.length > 1 && (
            <>
              <Button
                icon={ChevronLeft}
                color="ghost"
                aria-label="Previous"
                disabled={index <= 0}
                onClick={goPrev}
              />
              <span className="text-sm text-base-content/60">
                {index + 1} of {items.length}
              </span>
              <Button
                icon={ChevronRight}
                color="ghost"
                aria-label="Next"
                disabled={index >= items.length - 1}
                onClick={goNext}
              />
            </>
          )}
        </div>
        <Button onClick={() => mediaViewer.close()}>Close</Button>
      </Modal.Actions>
    </Modal>
  );
};

type MediaBodyProps = {
  item: MediaViewerItem;
  kind: MediaKind | null;
  src: string;
  onError: () => void;
};

const MediaBody = ({ item, kind, src, onError }: MediaBodyProps) => {
  if (kind === "image") {
    return (
      <img
        src={src}
        alt={item.objectKey}
        className="max-h-[70vh] mx-auto object-contain"
        onError={onError}
      />
    );
  }

  if (kind === "video") {
    return (
      <video src={src} controls className="max-h-[70vh] w-full" onError={onError} />
    );
  }

  if (kind === "audio") {
    return <audio src={src} controls className="w-full" onError={onError} />;
  }

  if (kind === "pdf") {
    return <iframe src={src} title={item.objectKey} className="w-full h-[70vh]" />;
  }

  if (kind === "text") {
    return <TextPreview src={src} onError={onError} />;
  }

  // Unclassifiable — should not happen in practice, since the item list the
  // viewer is opened with is pre-filtered by classifyMedia !== null, but stay
  // defensive rather than rendering nothing silently. Reporting the failure
  // is a side effect, so it runs in an effect rather than during render.
  return <UnclassifiedFallback onError={onError} />;
};

const UnclassifiedFallback = ({ onError }: { onError: () => void }) => {
  useEffect(() => {
    onError();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return null;
};

type TextPreviewProps = {
  src: string;
  onError: () => void;
};

const TextPreview = ({ src, onError }: TextPreviewProps) => {
  const [text, setText] = useState<string | null>(null);
  const onErrorRef = useRef(onError);
  onErrorRef.current = onError;

  useEffect(() => {
    setText(null);
    const controller = new AbortController();

    fetch(src, { credentials: "include", signal: controller.signal })
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.text();
      })
      .then((body) => {
        const truncated = body.length > TEXT_PREVIEW_LIMIT;
        setText(
          truncated
            ? body.slice(0, TEXT_PREVIEW_LIMIT) + "\n… (truncated)"
            : body
        );
      })
      .catch((err) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        onErrorRef.current();
      });

    return () => controller.abort();
  }, [src]);

  if (text === null) return null;

  return (
    <pre className="whitespace-pre-wrap break-all text-xs max-h-[70vh] overflow-auto">
      {text}
    </pre>
  );
};

export default MediaViewer;
