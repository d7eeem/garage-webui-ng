import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  getUploadItemPublicUrl,
  MAX_CONCURRENT_UPLOADS,
  resolveUploadContentType,
  uploadFile,
} from "./upload-queue";
import uploadQueue from "./upload-queue";
import type { Config, S3Web } from "@/types/garage";

const mkConfig = (s3_web?: Partial<S3Web>): Config =>
  s3_web ? { s3_web: s3_web as S3Web } : {};

/** Minimal fake XMLHttpRequest — no network is involved in any test here. */
class FakeXhr {
  status = 0;
  responseText = "";
  upload = { onprogress: null as ((e: ProgressEvent) => void) | null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;
  ontimeout: (() => void) | null = null;
  headers: Record<string, string> = {};
  withCredentials = false;
  aborted = false;
  method = "";
  url = "";
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  body: any = null;

  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(k: string, v: string) {
    this.headers[k] = v;
  }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  send(body: any) {
    this.body = body;
  }
  abort() {
    this.aborted = true;
    this.onabort?.();
  }
}

const makeFile = (name: string, size = 100) => {
  const file = new File([new Uint8Array(size)], name);
  return file;
};

describe("resolveUploadContentType", () => {
  it("preserves a non-empty fileType unchanged", () => {
    expect(resolveUploadContentType("photo.svg", "text/x-custom")).toBe(
      "text/x-custom"
    );
  });

  it("resolves .svg", () => {
    expect(resolveUploadContentType("homepage.svg", "")).toBe(
      "image/svg+xml"
    );
  });

  it("resolves .png", () => {
    expect(resolveUploadContentType("logo.png", "")).toBe("image/png");
  });

  it("resolves .jpg", () => {
    expect(resolveUploadContentType("photo.jpg", "")).toBe("image/jpeg");
  });

  it("resolves .jpeg", () => {
    expect(resolveUploadContentType("photo.jpeg", "")).toBe("image/jpeg");
  });

  it("resolves .webp", () => {
    expect(resolveUploadContentType("photo.webp", "")).toBe("image/webp");
  });

  it("resolves .gif", () => {
    expect(resolveUploadContentType("anim.gif", "")).toBe("image/gif");
  });

  it("resolves .css", () => {
    expect(resolveUploadContentType("styles.css", "")).toBe("text/css");
  });

  it("resolves .js", () => {
    expect(resolveUploadContentType("script.js", "")).toBe(
      "text/javascript"
    );
  });

  it("falls back to application/octet-stream for an unknown extension", () => {
    expect(resolveUploadContentType("data.unknownext", "")).toBe(
      "application/octet-stream"
    );
  });

  // mime/lite does not resolve .ico (unlike the full `mime` package); that
  // gap is closed authoritatively server-side by
  // resolveUploadContentType/mime.TypeByExtension in backend/router/browse.go,
  // which does cover .ico. This test documents the frontend-side fallback
  // rather than asserting a false "correct" mime type for .ico here.
  it("falls back to application/octet-stream for .ico (closed server-side)", () => {
    expect(resolveUploadContentType("favicon.ico", "")).toBe(
      "application/octet-stream"
    );
  });
});

describe("getUploadItemPublicUrl", () => {
  const publicUrlConfig = mkConfig({
    public_url: "https://{bucket}.web.ex.local",
  });

  it("builds the URL from item.bucket, not some other bucket name", () => {
    const url = getUploadItemPublicUrl(
      { bucket: "assets", key: "dashboard/homepage.svg" },
      true,
      publicUrlConfig
    );
    expect(url).toBe("https://assets.web.ex.local/dashboard/homepage.svg");
  });

  it("never builds from a different bucket than the item's own, even if one is passed nearby", () => {
    // Regression guard for the cross-bucket bug: an item belonging to
    // "other-bucket" must never produce a URL under "assets", no matter
    // what bucket name a caller happens to have in scope elsewhere.
    const url = getUploadItemPublicUrl(
      { bucket: "other-bucket", key: "file.png" },
      true,
      publicUrlConfig
    );
    expect(url).toBe("https://other-bucket.web.ex.local/file.png");
    expect(url).not.toContain("assets");
  });

  it("is null when the item's bucket has no anonymous read", () => {
    const url = getUploadItemPublicUrl(
      { bucket: "assets", key: "dashboard/homepage.svg" },
      false,
      publicUrlConfig
    );
    expect(url).toBeNull();
  });

  it("is null when anonymous read is on but no public URL can be built", () => {
    const url = getUploadItemPublicUrl(
      { bucket: "assets", key: "dashboard/homepage.svg" },
      true,
      undefined
    );
    expect(url).toBeNull();
  });
});

describe("uploadFile", () => {
  beforeEach(() => {
    document.cookie = "csrf_token=; expires=Thu, 01 Jan 1970 00:00:00 UTC;";
  });

  it("sends a PUT to the BASE_PATH-aware, percent-encoded URL", () => {
    let xhr!: FakeXhr;
    uploadFile({
      bucket: "my-bucket",
      key: "hp/my file #1.png",
      file: makeFile("my file #1.png"),
      onProgress: () => {},
      onSuccess: () => {},
      onError: () => {},
      createXhr: () => (xhr = new FakeXhr()) as unknown as XMLHttpRequest,
    });

    expect(xhr.method).toBe("PUT");
    expect(xhr.url).toContain(
      "/browse/my-bucket/hp/my%20file%20%231.png"
    );
  });

  it("attaches the X-CSRF-Token header", () => {
    document.cookie = "csrf_token=abc";
    let xhr!: FakeXhr;
    uploadFile({
      bucket: "b",
      key: "a.txt",
      file: makeFile("a.txt"),
      onProgress: () => {},
      onSuccess: () => {},
      onError: () => {},
      createXhr: () => (xhr = new FakeXhr()) as unknown as XMLHttpRequest,
    });

    expect(xhr.headers["X-CSRF-Token"]).toBe("abc");
  });

  it("sets withCredentials", () => {
    let xhr!: FakeXhr;
    uploadFile({
      bucket: "b",
      key: "a.txt",
      file: makeFile("a.txt"),
      onProgress: () => {},
      onSuccess: () => {},
      onError: () => {},
      createXhr: () => (xhr = new FakeXhr()) as unknown as XMLHttpRequest,
    });

    expect(xhr.withCredentials).toBe(true);
  });

  it("never sets Content-Type", () => {
    let xhr!: FakeXhr;
    uploadFile({
      bucket: "b",
      key: "a.txt",
      file: makeFile("a.txt"),
      onProgress: () => {},
      onSuccess: () => {},
      onError: () => {},
      createXhr: () => (xhr = new FakeXhr()) as unknown as XMLHttpRequest,
    });

    expect(xhr.headers["Content-Type"]).toBeUndefined();
  });

  it("reports progress", () => {
    let xhr!: FakeXhr;
    const onProgress = vi.fn();
    uploadFile({
      bucket: "b",
      key: "a.txt",
      file: makeFile("a.txt"),
      onProgress,
      onSuccess: () => {},
      onError: () => {},
      createXhr: () => (xhr = new FakeXhr()) as unknown as XMLHttpRequest,
    });

    xhr.upload.onprogress?.({
      lengthComputable: true,
      loaded: 50,
      total: 100,
    } as ProgressEvent);

    expect(onProgress).toHaveBeenCalledWith(50, 100);
  });

  it("surfaces the server's error text on a non-2xx", () => {
    let xhr!: FakeXhr;
    const onError = vi.fn();
    uploadFile({
      bucket: "b",
      key: "a.txt",
      file: makeFile("a.txt"),
      onProgress: () => {},
      onSuccess: () => {},
      onError,
      createXhr: () => (xhr = new FakeXhr()) as unknown as XMLHttpRequest,
    });

    xhr.status = 413;
    xhr.responseText = "upload is too large: ...";
    xhr.onload?.();

    expect(onError).toHaveBeenCalledWith("upload is too large: ...");
  });

  it("falls back to the status when the error body is empty", () => {
    let xhr!: FakeXhr;
    const onError = vi.fn();
    uploadFile({
      bucket: "b",
      key: "a.txt",
      file: makeFile("a.txt"),
      onProgress: () => {},
      onSuccess: () => {},
      onError,
      createXhr: () => (xhr = new FakeXhr()) as unknown as XMLHttpRequest,
    });

    xhr.status = 502;
    xhr.responseText = "";
    xhr.onload?.();

    expect(onError).toHaveBeenCalledTimes(1);
    expect(onError.mock.calls[0][0]).toContain("502");
  });

  it("turns a transport failure into a diagnosable message", () => {
    let xhr!: FakeXhr;
    const onError = vi.fn();
    uploadFile({
      bucket: "b",
      key: "a.txt",
      file: makeFile("a.txt"),
      onProgress: () => {},
      onSuccess: () => {},
      onError,
      createXhr: () => (xhr = new FakeXhr()) as unknown as XMLHttpRequest,
    });

    xhr.onerror?.();

    expect(onError).toHaveBeenCalledTimes(1);
    const message = onError.mock.calls[0][0];
    expect(message).not.toBe("");
    expect(message.toLowerCase()).toMatch(/size limit|reverse proxy/);
  });
});

describe("uploadQueue", () => {
  let instances: FakeXhr[] = [];
  const realXHR = globalThis.XMLHttpRequest;

  beforeEach(() => {
    instances = [];
    uploadQueue.setState({ items: [], completed: null, collapsed: false });

    class TrackedFakeXhr extends FakeXhr {
      constructor() {
        super();
        instances.push(this);
      }
    }

    // @ts-expect-error - test double, not a full XMLHttpRequest
    globalThis.XMLHttpRequest = TrackedFakeXhr;
  });

  afterEach(() => {
    globalThis.XMLHttpRequest = realXHR;
  });

  it("starts at most MAX_CONCURRENT_UPLOADS at once", () => {
    const files = Array.from({ length: 5 }, (_, i) => makeFile(`f${i}.txt`));
    uploadQueue.enqueue("bucket", "prefix/", files);

    const items = uploadQueue.getState().items;
    const uploading = items.filter((i) => i.status === "uploading");
    const queued = items.filter((i) => i.status === "queued");

    expect(uploading).toHaveLength(MAX_CONCURRENT_UPLOADS);
    expect(queued).toHaveLength(5 - MAX_CONCURRENT_UPLOADS);
    expect(instances).toHaveLength(MAX_CONCURRENT_UPLOADS);
  });

  it("completing one starts the next", () => {
    const files = Array.from({ length: 5 }, (_, i) => makeFile(`f${i}.txt`));
    uploadQueue.enqueue("bucket", "prefix/", files);

    instances[0].status = 200;
    instances[0].responseText = "";
    instances[0].onload?.();

    const items = uploadQueue.getState().items;
    expect(items.filter((i) => i.status === "queued")).toHaveLength(1);
    expect(items.filter((i) => i.status === "uploading")).toHaveLength(
      MAX_CONCURRENT_UPLOADS
    );
    expect(items.filter((i) => i.status === "done")).toHaveLength(1);
  });

  it("cancel on an in-flight item aborts it, marks it canceled, and a late onload does not resurrect it", () => {
    const files = [makeFile("f0.txt")];
    uploadQueue.enqueue("bucket", "prefix/", files);

    const id = uploadQueue.getState().items[0].id;
    const xhr = instances[0];

    uploadQueue.cancel(id);

    expect(xhr.aborted).toBe(true);
    let item = uploadQueue.getState().items.find((i) => i.id === id);
    expect(item?.status).toBe("canceled");

    // A late response arriving after cancellation must not resurrect it.
    xhr.status = 200;
    xhr.onload?.();

    item = uploadQueue.getState().items.find((i) => i.id === id);
    expect(item?.status).toBe("canceled");
  });

  it("a failed upload does not stall the queue", () => {
    const files = Array.from({ length: 5 }, (_, i) => makeFile(`f${i}.txt`));
    uploadQueue.enqueue("bucket", "prefix/", files);

    instances[0].status = 500;
    instances[0].responseText = "boom";
    instances[0].onload?.();

    const items = uploadQueue.getState().items;
    expect(items.filter((i) => i.status === "error")).toHaveLength(1);
    expect(items.filter((i) => i.status === "uploading")).toHaveLength(
      MAX_CONCURRENT_UPLOADS
    );
    expect(items.filter((i) => i.status === "queued")).toHaveLength(1);
  });

  it("a failed item keeps its File — retry resends it via a new transport", () => {
    uploadQueue.enqueue("bucket", "prefix/", [makeFile("f0.txt")]);
    const id = uploadQueue.getState().items[0].id;

    instances[0].status = 500;
    instances[0].responseText = "boom";
    instances[0].onload?.();
    expect(uploadQueue.getState().items[0].status).toBe("error");

    uploadQueue.retry(id);

    // A fresh transport started for the same row, immediately (a slot is
    // free) — proof the original File was retained rather than dropped by
    // the (now-split) cleanup on the error path.
    expect(instances).toHaveLength(2);
    expect(uploadQueue.getState().items[0].status).toBe("uploading");

    instances[1].status = 200;
    instances[1].onload?.();
    expect(uploadQueue.getState().items[0].status).toBe("done");
  });

  it("retry does not create a second row", () => {
    uploadQueue.enqueue("bucket", "prefix/", [makeFile("f0.txt")]);
    const id = uploadQueue.getState().items[0].id;

    instances[0].status = 500;
    instances[0].responseText = "boom";
    instances[0].onload?.();

    uploadQueue.retry(id);

    const items = uploadQueue.getState().items;
    expect(items).toHaveLength(1);
    expect(items[0].id).toBe(id);
  });

  it("retry no-ops on a non-error item", () => {
    uploadQueue.enqueue("bucket", "prefix/", [makeFile("f0.txt")]);
    const id = uploadQueue.getState().items[0].id;
    expect(uploadQueue.getState().items[0].status).toBe("uploading");

    uploadQueue.retry(id);

    expect(instances).toHaveLength(1);
    expect(uploadQueue.getState().items[0].status).toBe("uploading");
  });

  it("clearFinished removes only terminal rows, leaving active ones", () => {
    const files = [makeFile("f0.txt"), makeFile("f1.txt"), makeFile("f2.txt")];
    uploadQueue.enqueue("bucket", "prefix/", files);
    const ids = uploadQueue.getState().items.map((i) => i.id);

    instances[0].status = 200;
    instances[0].onload?.(); // f0 -> done

    instances[1].status = 500;
    instances[1].responseText = "boom";
    instances[1].onload?.(); // f1 -> error

    // f2 stays uploading.

    uploadQueue.clearFinished();

    const items = uploadQueue.getState().items;
    expect(items).toHaveLength(1);
    expect(items[0].id).toBe(ids[2]);
    expect(items[0].status).toBe("uploading");
  });

  it("the completion signal names the bucket that actually finished", () => {
    uploadQueue.enqueue("bucket-a", "p/", [makeFile("a.txt")]);
    uploadQueue.enqueue("bucket-b", "p/", [makeFile("b.txt")]);

    // Both start immediately — well under MAX_CONCURRENT_UPLOADS.
    instances[1].status = 200;
    instances[1].onload?.();

    expect(uploadQueue.getState().completed).toEqual({
      bucket: "bucket-b",
      seq: 1,
    });
  });

  it("toggleCollapsed changes only collapsed — handles and pendingFiles stay intact", () => {
    const files = [makeFile("f0.txt"), makeFile("f1.txt")];
    uploadQueue.enqueue("bucket", "prefix/", files);
    const [id0, id1] = uploadQueue.getState().items.map((i) => i.id);
    const itemsBefore = uploadQueue.getState().items;

    expect(uploadQueue.getState().collapsed).toBe(false);
    uploadQueue.toggleCollapsed();
    expect(uploadQueue.getState().collapsed).toBe(true);

    // Item statuses are untouched by the toggle.
    expect(uploadQueue.getState().items).toEqual(itemsBefore);

    // handles: cancel still aborts the real in-flight xhr.
    uploadQueue.cancel(id0);
    expect(instances[0].aborted).toBe(true);

    // pendingFiles: a failed item's File survived, so it can still be retried.
    instances[1].status = 500;
    instances[1].responseText = "boom";
    instances[1].onload?.();
    uploadQueue.retry(id1);
    expect(
      uploadQueue.getState().items.find((i) => i.id === id1)?.status
    ).toBe("uploading");
  });
});
