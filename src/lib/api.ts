import * as utils from "@/lib/utils";
import { BASE_PATH } from "./consts";

type FetchOptions = Omit<RequestInit, "headers" | "body"> & {
  params?: Record<string, any>;
  headers?: Record<string, string>;
  body?: any;
};

export const API_URL = BASE_PATH + "/api";

/**
 * Percent-encodes an object key for use in a `/browse/...` path.
 *
 * Each path segment is encoded individually so that `/` separators — which
 * delimit S3 "directories" — stay literal, while `?`, `#`, `%`, spaces, and
 * other structural characters inside a segment are escaped.
 *
 * The Go backend's counterpart is `browseObjectURL` in
 * `backend/router/browse.go`. `encodeURIComponent` and Go's `url.PathEscape`
 * are not byte-identical (they differ on `!'()*`), but both round-trip
 * correctly through the server's `r.PathValue`, which is the only property
 * that matters — do not try to make the two byte-identical.
 */
export const encodeObjectPath = (key: string) =>
  key
    .split("/")
    .map((segment) => encodeURIComponent(segment))
    .join("/");

/**
 * Reads a cookie by name. Only used for the CSRF token, which is hex and so
 * needs no decoding.
 */
const readCookie = (name: string) =>
  document.cookie
    .split("; ")
    .find((c) => c.startsWith(name + "="))
    ?.split("=")[1];

/**
 * Name of the double-submit CSRF cookie/header pair. The server issues the
 * cookie (deliberately not HttpOnly) on any read and requires the header on
 * every write except `POST /auth/login` and `POST /setup`.
 *
 * Kept in sync with `backend/middleware/csrf.go`.
 */
const CSRF_COOKIE_NAME = "csrf_token";
const CSRF_HEADER_NAME = "X-CSRF-Token";

export class APIError extends Error {
  status!: number;

  constructor(message: string, status: number = 400) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}

const api = {
  async fetch<T = any>(url: string, options?: Partial<FetchOptions>) {
    // Sent on every request, reads included: it is harmless where the server
    // does not check it, and attaching it in one place means no caller can
    // forget it on a write.
    const headers: Record<string, string> = {
      [CSRF_HEADER_NAME]: readCookie(CSRF_COOKIE_NAME) ?? "",
    };
    const _url = new URL(API_URL + url, window.location.origin);

    if (options?.params) {
      Object.entries(options.params).forEach(([key, value]) => {
        _url.searchParams.set(key, String(value));
      });
    }

    if (
      typeof options?.body === "object" &&
      !(options.body instanceof FormData)
    ) {
      options.body = JSON.stringify(options.body);
      headers["Content-Type"] = "application/json";
    }

    const res = await fetch(_url, {
      ...options,
      credentials: "include",
      headers: { ...headers, ...(options?.headers || {}) },
    });

    const isJson = res.headers
      .get("Content-Type")
      ?.includes("application/json");
    const data = isJson ? await res.json() : await res.text();

    if (res.status === 401 && !url.startsWith("/auth")) {
      window.location.href = utils.url("/auth/login");
      throw new APIError("unauthorized", res.status);
    }

    if (!res.ok) {
      const message = isJson
        ? data?.message
        : typeof data === "string"
        ? data
        : res.statusText;
      throw new APIError(message, res.status);
    }

    return data as unknown as T;
  },

  async get<T = any>(url: string, options?: Partial<FetchOptions>) {
    return this.fetch<T>(url, {
      ...options,
      method: "GET",
    });
  },

  async post<T = any>(url: string, options?: Partial<FetchOptions>) {
    return this.fetch<T>(url, {
      ...options,
      method: "POST",
    });
  },

  async put<T = any>(url: string, options?: Partial<FetchOptions>) {
    return this.fetch<T>(url, {
      ...options,
      method: "PUT",
    });
  },

  async delete<T = any>(url: string, options?: Partial<FetchOptions>) {
    return this.fetch<T>(url, {
      ...options,
      method: "DELETE",
    });
  },
};

export default api;
