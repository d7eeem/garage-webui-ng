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
 * Vhost style when the template contains "{bucket}", path style otherwise.
 * Returns null when no override is configured.
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
  return `${template}/${bucketName}`;
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
  return `${base}/${key}`;
}
