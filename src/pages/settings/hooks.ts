import api from "@/lib/api";
import { useMutation, UseMutationOptions, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { ChangePasswordSchema } from "./schema";

/** GET /update-check response shape — see backend/router/update.go. */
export type UpdateCheck = {
  enabled: boolean;
  current: string;
  latest?: string;
  url?: string;
  updateAvailable?: boolean;
  checkFailed?: boolean;
  deployment?: "binary" | "managed" | "unknown";
  updateCommand?: string;
  canSelfUpdate?: boolean;
};

/** POST /update/apply response shape — see backend/router/selfupdate.go. */
export type ApplyUpdateResult = {
  version: string;
  restartRequired: boolean;
  restarting: boolean;
  backupPath?: string;
};

type Options = UseMutationOptions<unknown, Error, ChangePasswordSchema>;

/**
 * Changes the signed-in user's own password. The endpoint takes no user id:
 * the server acts on whoever the session names, so this hook can never target
 * another account.
 *
 * `options` is spread first and the callbacks are composed on top, rather than
 * the usual `...options` last: the toasts are part of this hook's contract and
 * a caller adding its own `onSuccess` (to clear the form) must not silently
 * drop them.
 */
export const useChangePassword = (options?: Options) => {
  return useMutation<unknown, Error, ChangePasswordSchema>({
    ...options,
    mutationFn: (body) => api.post("/auth/change-password", { body }),
    onSuccess: (data, variables, context) => {
      toast.success("Password changed");
      options?.onSuccess?.(data, variables, context);
    },
    onError: (err, variables, context) => {
      toast.error(err?.message || "Unknown error");
      options?.onError?.(err, variables, context);
    },
  });
};

/**
 * Whether a newer release exists. The server caches this for 6h
 * (UPDATE_CHECK_ENABLED, see backend/router/update.go), so a 1h staleTime
 * just avoids re-asking on every mount of the About tab within a session —
 * it does not need to match the server's cache window.
 */
export const useUpdateCheck = () =>
  useQuery({
    queryKey: ["update-check"],
    queryFn: () => api.get<UpdateCheck>("/update-check"),
    staleTime: 60 * 60 * 1000,
    retry: false,
  });

type ApplyUpdateOptions = UseMutationOptions<
  ApplyUpdateResult,
  Error,
  { restart: boolean }
>;

/**
 * Downloads, verifies and stages the latest release binary
 * (backend/router/selfupdate.go). The server default (`restart: false`) only
 * swaps the binary on disk; the running process keeps serving the old
 * version until an operator restarts it. `restart: true` additionally asks
 * the server to trigger its own graceful shutdown once the swap succeeds —
 * callers must obtain their own explicit confirmation for that, separate
 * from the confirmation for the update itself.
 */
export const useApplyUpdate = (options?: ApplyUpdateOptions) =>
  useMutation<ApplyUpdateResult, Error, { restart: boolean }>({
    ...options,
    mutationFn: (body) => api.post("/update/apply", { body }),
  });
