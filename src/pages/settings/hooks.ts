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
