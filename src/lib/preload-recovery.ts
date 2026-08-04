/**
 * Recovery from stale dynamic-import chunks after an upgrade.
 *
 * Every route is a lazy chunk, so the loaded `index.html` names content-hashed
 * files like `page-Bb-knZwX.js`. Upgrading the binary swaps `dist/` for one with
 * new hashes, and a tab still running the previous build then asks for chunks
 * that no longer exist. Vite raises `vite:preloadError`; reloading fetches the
 * new `index.html` and, with it, the new chunk names.
 *
 * The reload is guarded by a `sessionStorage` timestamp. If a reload was already
 * attempted moments ago, the *fresh* `index.html` failed too — the deployment is
 * genuinely broken rather than merely stale — so the error is left to reach the
 * error boundary, which is the honest outcome. Without that guard the browser
 * would reload forever.
 */

const RELOAD_FLAG = "gwui:chunk-reload";

/**
 * A second preload failure within this window means the reload did not help.
 * Beyond it, the flag has effectively expired, so a *later* upgrade in the same
 * tab session can still recover on its own.
 */
export const RELOAD_COOLDOWN_MS = 10_000;

/** Timestamp of the last reload attempt, or null if there is none to trust. */
const readLastAttempt = (): number | null => {
  try {
    const raw = window.sessionStorage.getItem(RELOAD_FLAG);
    if (!raw) return null;
    const at = Number.parseInt(raw, 10);
    return Number.isFinite(at) ? at : null;
  } catch {
    // Storage blocked (private mode, cookies disabled). markAttempt below is
    // what actually gates the reload, so failing "no attempt" is safe here.
    return null;
  }
};

/**
 * Records a reload attempt. Returns false when it could not be persisted — in
 * that case there is no way to detect a second failure, so the caller must not
 * reload at all rather than risk an unbounded loop.
 */
const markAttempt = (now: number): boolean => {
  try {
    window.sessionStorage.setItem(RELOAD_FLAG, String(now));
    return true;
  } catch {
    return false;
  }
};

export const handlePreloadError = (event: Event): void => {
  const now = Date.now();
  const last = readLastAttempt();

  // Already reloaded once for this failure — stop, and let the app surface it.
  if (last !== null && now - last < RELOAD_COOLDOWN_MS) return;

  if (!markAttempt(now)) return;

  event.preventDefault(); // suppress Vite's unhandled rejection
  window.location.reload();
};

/** Installs the listener. Returns a disposer (used by tests). */
export const installPreloadRecovery = (): (() => void) => {
  window.addEventListener("vite:preloadError", handlePreloadError);
  return () =>
    window.removeEventListener("vite:preloadError", handlePreloadError);
};
