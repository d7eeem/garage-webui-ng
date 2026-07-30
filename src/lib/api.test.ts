import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import api, { APIError, encodeObjectPath } from "@/lib/api";

type MockResponseInit = {
  status: number;
  contentType?: string;
  body: string | object;
};

const mockResponse = ({ status, contentType, body }: MockResponseInit) => {
  const headers = new Headers();
  if (contentType) headers.set("Content-Type", contentType);

  const text = typeof body === "string" ? body : JSON.stringify(body);

  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: "",
    headers,
    json: async () => (typeof body === "string" ? JSON.parse(body) : body),
    text: async () => text,
  } as unknown as Response;
};

describe("api error handling", () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("resolves with parsed JSON on a successful JSON response", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(
      mockResponse({
        status: 200,
        contentType: "application/json",
        body: { ok: true },
      })
    );

    await expect(api.get("/x")).resolves.toEqual({ ok: true });
  });

  it("rejects with an APIError using the plain-text body as the message", async () => {
    // This is the shape the Go backend actually produces:
    // backend/utils/utils.go writes err.Error() as a bare body with no
    // Content-Type header.
    vi.mocked(fetch).mockResolvedValueOnce(
      mockResponse({ status: 500, body: "boom" })
    );

    const error: unknown = await api.get("/x").catch((err) => err);

    expect(error).toBeInstanceOf(APIError);
    expect((error as APIError).message).toBe("boom");
    expect((error as APIError).status).toBe(500);
  });

  it("always sends credentials: include", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(
      mockResponse({
        status: 200,
        contentType: "application/json",
        body: { ok: true },
      })
    );

    await api.get("/x");

    const init = vi.mocked(fetch).mock.calls[0][1];
    expect(init).toMatchObject({ credentials: "include" });
  });

  it("serializes query params onto the request URL", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(
      mockResponse({
        status: 200,
        contentType: "application/json",
        body: { ok: true },
      })
    );

    await api.get("/x", { params: { a: 1 } });

    const requestUrl = vi.mocked(fetch).mock.calls[0][0] as URL;
    expect(requestUrl.search).toBe("?a=1");
  });

  // Not covered: the 401 branch assigns `window.location.href`, which jsdom
  // handles inconsistently across versions and would make this suite flaky.
});

describe("encodeObjectPath", () => {
  it("leaves a plain filename untouched", () => {
    expect(encodeObjectPath("file.txt")).toBe("file.txt");
  });

  it("preserves '/' separators between segments", () => {
    expect(encodeObjectPath("dir/file.txt")).toBe("dir/file.txt");
  });

  it("encodes spaces and '#'", () => {
    expect(encodeObjectPath("report #3.pdf")).toBe("report%20%233.pdf");
  });

  it("encodes '?'", () => {
    expect(encodeObjectPath("a?b.txt")).toBe("a%3Fb.txt");
  });

  it("encodes a literal '%'", () => {
    expect(encodeObjectPath("100%.txt")).toBe("100%25.txt");
  });

  it("returns an empty string for an empty key", () => {
    expect(encodeObjectPath("")).toBe("");
  });

  // encodeURIComponent (JS) and url.PathEscape (Go, in
  // backend/router/browse.go) intentionally are not byte-identical — they
  // differ on `!'()*`. Both round-trip correctly through the server's
  // r.PathValue, which is the only property that matters. Do not "fix" this
  // by trying to align the two encoders.
});

describe("api client end-to-end URL encoding", () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("builds a percent-encoded pathname and a separate query string for a key with '?' and '#'", async () => {
    vi.mocked(fetch).mockResolvedValueOnce(
      mockResponse({
        status: 200,
        contentType: "application/json",
        body: { ok: true },
      })
    );

    await api.get("/browse/b/" + encodeObjectPath("a?b#c.txt"), {
      params: { view: 1 },
    });

    const requestUrl = vi.mocked(fetch).mock.calls[0][0] as URL;
    expect(requestUrl.pathname).toBe("/api/browse/b/a%3Fb%23c.txt");
    expect(requestUrl.search).toBe("?view=1");
  });
});
