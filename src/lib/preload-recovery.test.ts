import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  RELOAD_COOLDOWN_MS,
  installPreloadRecovery,
} from "@/lib/preload-recovery";

/** Dispatches the event Vite raises when a dynamic import fails. */
const firePreloadError = () => {
  const event = new Event("vite:preloadError", { cancelable: true });
  window.dispatchEvent(event);
  return event;
};

describe("preload recovery", () => {
  let reload: ReturnType<typeof vi.fn>;
  let dispose: () => void;

  beforeEach(() => {
    window.sessionStorage.clear();
    reload = vi.fn();
    // jsdom's location.reload is not writable; replace the accessor.
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { ...window.location, reload },
    });
    dispose = installPreloadRecovery();
  });

  afterEach(() => {
    dispose();
    vi.restoreAllMocks();
    window.sessionStorage.clear();
  });

  it("reloads once on the first preload failure", () => {
    const event = firePreloadError();

    expect(reload).toHaveBeenCalledTimes(1);
    expect(event.defaultPrevented).toBe(true);
  });

  it("does not reload again on an immediate second failure", () => {
    firePreloadError();
    expect(reload).toHaveBeenCalledTimes(1);

    // Simulates the reloaded page failing all over again: a broken deployment,
    // not a stale tab. This must dead-end, not loop.
    const second = firePreloadError();

    expect(reload).toHaveBeenCalledTimes(1);
    expect(second.defaultPrevented).toBe(false);
  });

  it("survives many rapid failures without a reload loop", () => {
    for (let i = 0; i < 20; i++) firePreloadError();

    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("recovers again once the cooldown has elapsed", () => {
    firePreloadError();
    expect(reload).toHaveBeenCalledTimes(1);

    vi.spyOn(Date, "now").mockReturnValue(Date.now() + RELOAD_COOLDOWN_MS + 1);

    firePreloadError();
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it("does not reload when sessionStorage cannot be written", () => {
    const real = Object.getOwnPropertyDescriptor(window, "sessionStorage");
    // jsdom's sessionStorage is a proxy that cannot be spied on, so swap the
    // whole property for one that throws the way a blocked store does.
    Object.defineProperty(window, "sessionStorage", {
      configurable: true,
      get: () => ({
        getItem: () => {
          throw new Error("storage disabled");
        },
        setItem: () => {
          throw new Error("storage disabled");
        },
      }),
    });

    try {
      firePreloadError();
      // No way to record the attempt means no way to detect a second failure,
      // so reloading at all would risk an unbounded loop.
      expect(reload).not.toHaveBeenCalled();
    } finally {
      if (real) Object.defineProperty(window, "sessionStorage", real);
    }
  });
});
