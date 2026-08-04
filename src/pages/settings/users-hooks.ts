import api from "@/lib/api";
import {
  useMutation,
  UseMutationOptions,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { CreateUserSchema, Role, User } from "./users-schema";

/**
 * Query key for the user roster. Every mutation below invalidates it, so the
 * table reflects the server's view rather than an optimistic guess — the server
 * may refuse a change (see the lockout guards in
 * `backend/router/admin_users.go`), and it is authoritative.
 */
export const usersQueryKey = ["admin-users"];

export const useUsers = (enabled = true) => {
  return useQuery({
    queryKey: usersQueryKey,
    queryFn: () => api.get<User[]>("/admin/users"),
    enabled,
  });
};

/**
 * Builds the mutation options shared by every write below: `options` is spread
 * FIRST and the invalidation is composed on top, rather than the usual
 * `...options` last. Refreshing the roster is part of each hook's contract, and
 * every caller in the Users tab passes its own `onSuccess` to close a dialog —
 * spreading last would silently drop the refetch and leave the table stale.
 */
const withInvalidate = <TData, TVars>(
  invalidate: () => void,
  options?: UseMutationOptions<TData, Error, TVars>
): Omit<UseMutationOptions<TData, Error, TVars>, "mutationFn"> => ({
  ...options,
  onSuccess: (data, variables, context) => {
    invalidate();
    options?.onSuccess?.(data, variables, context);
  },
});

/** Refetches the roster after a successful mutation. */
const useInvalidateUsers = () => {
  const queryClient = useQueryClient();
  return () => {
    queryClient.invalidateQueries({ queryKey: usersQueryKey });
  };
};

export const useCreateUser = (
  options?: UseMutationOptions<User, Error, CreateUserSchema>
) => {
  const invalidate = useInvalidateUsers();
  return useMutation<User, Error, CreateUserSchema>({
    mutationFn: (body) => api.post<User>("/admin/users", { body }),
    ...withInvalidate(invalidate, options),
  });
};

/** The fields PATCH /admin/users/{id} accepts. Omitted keys are left alone. */
export type UpdateUserPayload = {
  id: number;
  username?: string;
  role?: Role;
  disabled?: boolean;
};

export const useUpdateUser = (
  options?: UseMutationOptions<User, Error, UpdateUserPayload>
) => {
  const invalidate = useInvalidateUsers();
  return useMutation<User, Error, UpdateUserPayload>({
    // `api` has no patch() helper, so this goes through fetch() directly rather
    // than widening the shared client for a single caller.
    mutationFn: ({ id, ...body }) =>
      api.fetch<User>(`/admin/users/${id}`, { method: "PATCH", body }),
    ...withInvalidate(invalidate, options),
  });
};

export const useDeleteUser = (
  options?: UseMutationOptions<boolean, Error, number>
) => {
  const invalidate = useInvalidateUsers();
  return useMutation<boolean, Error, number>({
    mutationFn: (id) => api.delete<boolean>(`/admin/users/${id}`),
    ...withInvalidate(invalidate, options),
  });
};

export type ResetPasswordPayload = {
  id: number;
  newPassword: string;
};

/**
 * Sets another user's password. The response carries nothing but `true` — the
 * server never returns a password or a hash, so there is nothing here to
 * display, copy or cache.
 */
export const useResetPassword = (
  options?: UseMutationOptions<boolean, Error, ResetPasswordPayload>
) => {
  const invalidate = useInvalidateUsers();
  return useMutation<boolean, Error, ResetPasswordPayload>({
    mutationFn: ({ id, newPassword }) =>
      api.post<boolean>(`/admin/users/${id}/reset-password`, {
        body: { newPassword },
      }),
    ...withInvalidate(invalidate, options),
  });
};
