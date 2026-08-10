import { createStore, useStore } from "zustand";
import mime from "mime/lite";
import { apiUrl, csrfHeader, encodeObjectPath } from "@/lib/api";
import { UploadItem } from "./types";

/**
 * Resolves the Content-Type an upload should be sent with.
 *
 * A browser derives the multipart part's Content-Type from `File.type`,
 * which is the empty string for any extension the OS's local mime database
 * does not know — a frequent gap for `.svg`, `.webp`, `.avif` and `.ico`,
 * meaning those files were previously uploaded (and served back) with no
 * usable type. A non-empty `fileType` — what the browser already knows — is
 * always preserved unchanged; only an empty one falls back to a lookup by
 * filename extension via `mime/lite` (already a dependency; `object-list.tsx`
 * uses the same module for icon/thumbnail selection).
 *
 * `mime/lite` itself does not resolve `.ico` (returns null), so a `.ico`
 * upload still falls through to `application/octet-stream` here. That gap is
 * closed authoritatively server-side: `resolveUploadContentType` in
 * `backend/router/browse.go` re-resolves from the object key's extension via
 * Go's stdlib `mime.TypeByExtension`, which does cover `.ico`, whenever the
 * incoming Content-Type is empty or the generic `application/octet-stream`.
 */
export function resolveUploadContentType(
  fileName: string,
  fileType: string
): string {
  if (fileType) return fileType;
  return mime.getType(fileName) ?? "application/octet-stream";
}

/** How many uploads run at once. */
export const MAX_CONCURRENT_UPLOADS = 3;

export type UploadHandle = { abort: () => void };

export type UploadFileArgs = {
  bucket: string;
  key: string;
  file: File;
  onProgress: (loaded: number, total: number) => void;
  onSuccess: () => void;
  onError: (message: string) => void;
  /** Injectable for tests; defaults to the global. */
  createXhr?: () => XMLHttpRequest;
};

/**
 * Uploads a single file via XMLHttpRequest, not `fetch`: `fetch` has no
 * `onprogress` for the request body, so it cannot report upload progress.
 * `XMLHttpRequest` is the only web API that exposes `upload.onprogress`.
 *
 * Replicates the four things `api.fetch` does that a plain XHR PUT would
 * otherwise drop: `credentials: "include"` (-> `withCredentials`), the
 * `X-CSRF-Token` header (`middleware.CSRF` 403s a write without it), the
 * `BASE_PATH`-aware URL (`apiUrl`), and never setting the multipart
 * boundary's content-type header itself, so the browser can add it.
 */
export const uploadFile = (args: UploadFileArgs): UploadHandle => {
  const { bucket, key, file, onProgress, onSuccess, onError, createXhr } =
    args;

  const xhr = createXhr ? createXhr() : new XMLHttpRequest();
  const url = apiUrl(`/browse/${bucket}/${encodeObjectPath(key)}`);

  const form = new FormData();
  const contentType = resolveUploadContentType(file.name, file.type);
  const uploadable =
    contentType === file.type
      ? file
      : new File([file], file.name, { type: contentType });
  form.append("file", uploadable);

  xhr.open("PUT", url, true);
  xhr.withCredentials = true;

  const headers = csrfHeader();
  for (const [name, value] of Object.entries(headers)) {
    xhr.setRequestHeader(name, value);
  }

  xhr.upload.onprogress = (e) => {
    if (e.lengthComputable) {
      onProgress(e.loaded, e.total);
    }
  };

  xhr.onload = () => {
    if (xhr.status >= 200 && xhr.status < 300) {
      // The final `progress` event does not always fire at exactly 100%.
      onProgress(file.size, file.size);
      onSuccess();
      return;
    }

    // Error responses from this backend are plain text (see
    // backend/utils/utils.go), so `responseText` on a non-2xx *is* the
    // message.
    const body = (xhr.responseText || "").trim();
    onError(body || `Upload failed with status ${xhr.status}`);
  };

  xhr.onerror = () =>
    onError(
      "The connection dropped before the server replied. The file may exceed a " +
        "size limit on the server or a reverse proxy in front of it."
    );

  // Not set: xhr.timeout stays 0 (no timeout), which is correct for large
  // uploads. This handler exists only for completeness.
  xhr.ontimeout = () => onError("Upload timed out.");

  // Cancellation is driven by the store, which sets the item's status itself
  // before calling abort() — this handler must not overwrite that with an
  // error.
  xhr.onabort = () => {};

  xhr.send(form);

  return { abort: () => xhr.abort() };
};

type UploadQueueState = {
  items: UploadItem[];
  completedCount: number;
};

const store = createStore<UploadQueueState>(() => ({
  items: [],
  completedCount: 0,
}));

/** In-flight transports, keyed by item id. Not serializable state, so it
 * lives outside the store and must not trigger re-renders. */
const handles = new Map<string, UploadHandle>();

/** Files awaiting/undergoing upload, keyed by item id. `UploadItem` itself
 * carries no `File` (it is not serializable state either), so this map is
 * the only place the pump can find the bytes to send. */
const pendingFiles = new Map<string, File>();

let idCounter = 0;
const nextId = () => `upload-${Date.now()}-${idCounter++}`;

/**
 * Applies `updater` to the item with `id`, if it is still present. `updater`
 * returning `null` means "no-op" — the guard every terminal callback uses to
 * avoid resurrecting an item a user already canceled.
 */
const patchItem = (
  id: string,
  updater: (item: UploadItem) => UploadItem | null
): boolean => {
  let patched = false;

  store.setState((state) => {
    const index = state.items.findIndex((item) => item.id === id);
    if (index === -1) return state;

    const next = updater(state.items[index]);
    if (next === null) return state;

    patched = true;
    const items = state.items.slice();
    items[index] = next;
    return { items };
  });

  return patched;
};

const cleanup = (id: string) => {
  handles.delete(id);
  pendingFiles.delete(id);
};

/** While there is a free concurrency slot and a queued item, starts it. */
const pump = () => {
  const state = store.getState();
  const uploadingCount = state.items.filter(
    (item) => item.status === "uploading"
  ).length;
  const slots = MAX_CONCURRENT_UPLOADS - uploadingCount;
  if (slots <= 0) return;

  const toStart = state.items
    .filter((item) => item.status === "queued")
    .slice(0, slots);
  if (toStart.length === 0) return;

  const startIds = new Set(toStart.map((item) => item.id));
  store.setState((s) => ({
    items: s.items.map((item) =>
      startIds.has(item.id) ? { ...item, status: "uploading" } : item
    ),
  }));

  for (const item of toStart) {
    const file = pendingFiles.get(item.id);
    if (!file) {
      // Should not happen: enqueue always populates pendingFiles before the
      // item can reach "queued". Guard anyway rather than crash the pump.
      patchItem(item.id, (cur) =>
        cur.status === "canceled"
          ? null
          : { ...cur, status: "error", error: "Upload data was lost." }
      );
      continue;
    }

    const handle = uploadFile({
      bucket: item.bucket,
      key: item.key,
      file,
      onProgress: (loaded) => {
        patchItem(item.id, (cur) =>
          cur.status !== "uploading" ? null : { ...cur, loaded }
        );
      },
      onSuccess: () => {
        cleanup(item.id);
        const patched = patchItem(item.id, (cur) =>
          cur.status === "canceled"
            ? null
            : { ...cur, status: "done", loaded: cur.size }
        );
        if (patched) {
          store.setState((s) => ({ completedCount: s.completedCount + 1 }));
        }
        pump();
      },
      onError: (message) => {
        cleanup(item.id);
        patchItem(item.id, (cur) =>
          cur.status === "canceled"
            ? null
            : { ...cur, status: "error", error: message }
        );
        pump();
      },
    });

    handles.set(item.id, handle);
  }
};

const enqueue = (bucket: string, prefix: string, files: File[]) => {
  const newItems: UploadItem[] = files.map((file) => {
    const id = nextId();
    pendingFiles.set(id, file);
    return {
      id,
      key: prefix + file.name,
      name: file.name,
      bucket,
      size: file.size,
      loaded: 0,
      status: "queued",
    };
  });

  store.setState((state) => ({ items: [...state.items, ...newItems] }));
  pump();
};

const cancel = (id: string) => {
  handles.get(id)?.abort();
  cleanup(id);

  patchItem(id, (cur) =>
    cur.status === "done" || cur.status === "error"
      ? null
      : { ...cur, status: "canceled" }
  );

  pump();
};

const dismiss = (id: string) => {
  cleanup(id);
  store.setState((state) => ({
    items: state.items.filter((item) => item.id !== id),
  }));
};

const clearFinished = () => {
  store.setState((state) => ({
    items: state.items.filter(
      (item) =>
        item.status !== "done" &&
        item.status !== "error" &&
        item.status !== "canceled"
    ),
  }));
};

const uploadQueue = {
  ...store,
  enqueue,
  cancel,
  dismiss,
  clearFinished,
};

export default uploadQueue;

export const useUploadQueue = () => useStore(store);
