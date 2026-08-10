import { encodeObjectPath } from "@/lib/api";
import type { Config } from "@/types/garage";

/**
 * Garage serves a bucket as a static website at
 *   http://<bucket-global-alias><root_domain>[:<web-port>]/
 * where <root_domain> comes from the garage.toml [s3_web] block and, by Garage
 * convention, begins with a dot (e.g. ".web.example.com"). Website serving is
 * plain HTTP on the web bind port; HTTPS needs an external reverse proxy.
 *
 * An operator running behind such a proxy can set S3_WEB_PUBLIC_URL
 * (delivered as config.s3_web.public_url) to override this derived address
 * entirely — see applyPublicUrlTemplate below. Only when that override is
 * absent do we fall back to deriving http://<bucket><root_domain>:<port>.
 */

/**
 * True when Garage has a working public website URL: either an operator
 * override (S3_WEB_PUBLIC_URL) or an [s3_web] root_domain in garage.toml.
 */
export function isWebsiteHostingConfigured(config?: Config): boolean {
  return (
    !!config?.s3_web?.public_url?.trim() ||
    !!config?.s3_web?.root_domain?.trim()
  );
}

/**
 * Applies the operator-declared public base URL (S3_WEB_PUBLIC_URL, delivered
 * as config.s3_web.public_url) to a bucket name.
 *
 * The template must contain a "{bucket}" token. Returns null when no
 * override is configured, or when the configured template has no "{bucket}"
 * token to substitute.
 */
function applyPublicUrlTemplate(
  bucketName: string,
  config?: Config
): string | null {
  const template = config?.s3_web?.public_url?.trim().replace(/\/+$/, "");
  if (!template) return null;
  if (template.includes("{bucket}")) {
    return template.split("{bucket}").join(bucketName);
  }
  // No {bucket} token means we cannot address the bucket at all. Garage's
  // website endpoint resolves the bucket from the Host header only; a leading
  // path segment is consumed by nothing and always 404s. Returning null makes
  // getPublicAccess report "public-no-url", which tells the operator to fix
  // their configuration instead of handing them a link that cannot work.
  return null;
}

/**
 * Public base URL at which `bucketName` is served as a website, or null when
 * the bucket name is empty and no working public URL exists — either an
 * operator override (S3_WEB_PUBLIC_URL) or Garage's own [s3_web] root_domain
 * (callers should show guidance when this returns null).
 */
export function getBucketWebsiteBaseUrl(
  bucketName: string,
  config?: Config
): string | null {
  const name = bucketName?.trim();
  if (!name) return null;

  const override = applyPublicUrlTemplate(name, config);
  if (override) return override;

  const rawRoot = config?.s3_web?.root_domain?.trim();
  if (!rawRoot) return null;

  // Garage convention: root_domain starts with a dot. Normalise so an operator
  // who omitted it still gets a valid host.
  const root = rawRoot.startsWith(".") ? rawRoot : `.${rawRoot}`;

  // Web bind_addr looks like "[::]:3902" or "0.0.0.0:3902"; take the trailing port.
  const port = config?.s3_web?.bind_addr?.split(":").pop()?.trim();
  const portSuffix = port && port !== "80" && port !== "443" ? `:${port}` : "";

  return `http://${name}${root}${portSuffix}`;
}

/** Public URL of a single object served via the bucket's website, or null. */
export function getBucketWebsiteObjectUrl(
  bucketName: string,
  objectKey: string,
  config?: Config
): string | null {
  const base = getBucketWebsiteBaseUrl(bucketName, config);
  if (base == null) return null;
  const key = (objectKey ?? "").replace(/^\/+/, "");
  return `${base}/${encodeObjectPath(key)}`;
}

/**
 * Whether — and where — a bucket's objects are anonymously readable.
 *
 * Anonymous read is gated exclusively by Garage's website-endpoint toggle
 * (`bucket.websiteAccess`); a configured public base URL alone never implies
 * public access. Checked in this order:
 *
 *   1. `websiteAccess !== true` -> "private", full stop.
 *   2. no working public base URL -> "public-no-url" (access is open, but no
 *      URL can be shown/copied until the operator configures one).
 *   3. otherwise -> "public" with the object's URL.
 */
export type PublicAccess =
  | { state: "public"; url: string }
  | { state: "public-no-url" }
  | { state: "private" };

export function getPublicAccess(
  websiteAccess: boolean | undefined | null,
  bucketName: string,
  objectKey: string,
  config?: Config
): PublicAccess {
  if (websiteAccess !== true) return { state: "private" };

  const url = getBucketWebsiteObjectUrl(bucketName, objectKey, config);
  if (url == null) return { state: "public-no-url" };

  return { state: "public", url };
}
