// consts.ts

/**
 * Base path the UI is mounted under, injected by the Go server into a meta tag
 * (backend/ui/ui_prod.go rewrites "%BASE_PATH%").
 *
 * A meta tag rather than an inline <script> so the Content-Security-Policy can
 * use a strict `script-src 'self'` with no inline allowance.
 *
 * The placeholder guard is load-bearing: a DIRECT request for /index.html is
 * served straight from the embedded filesystem without substitution, so the
 * literal "%BASE_PATH%" can reach the browser. Treating it as empty keeps the
 * app working on that path instead of prefixing every URL with a placeholder.
 */
export const readBasePath = (): string => {
  const raw = document
    .querySelector('meta[name="app-base-path"]')
    ?.getAttribute("content")
    ?.trim();
  if (!raw || raw === "%BASE_PATH%") return "";
  return raw;
};

export const BASE_PATH =
  (import.meta.env.PROD ? readBasePath() : "") ||
  import.meta.env.VITE_BASE_PATH ||
  "";
