import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MAX_CONCURRENT_UPLOADS, uploadFile } from "./upload-queue";
import uploadQueue from "./upload-queue";

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
    uploadQueue.setState({ items: [], completedCount: 0 });

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
});
