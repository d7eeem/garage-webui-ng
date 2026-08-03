import type { Config } from "@/types/garage";

/**
 * Garage serves a bucket as a static website at
 *   http://<bucket-global-alias><root_domain>[:<web-port>]/
 * where <root_domain> comes from the garage.toml [s3_web] block and, by Garage
 * convention, begins with a dot (e.g. ".web.example.com"). Website serving is
 * plain HTTP on the web bind port; HTTPS needs an external reverse proxy, so we
 * always emit http:// here.
 */

/** True when Garage has a web endpoint configured (an [s3_web] root_domain). */
export function isWebsiteHostingConfigured(config?: Config): boolean {
  return !!config?.s3_web?.root_domain?.trim();
}

/**
 * Public base URL at which `bucketName` is served as a website, or null when the
 * bucket name is empty or Garage has no [s3_web] root_domain configured (no
 * working public URL exists then — callers should show guidance instead).
 */
export function getBucketWebsiteBaseUrl(
  bucketName: string,
  config?: Config
): string | null {
  const name = bucketName?.trim();
  const rawRoot = config?.s3_web?.root_domain?.trim();
  if (!name || !rawRoot) return null;

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
