import api, { APIError, encodeObjectPath } from "@/lib/api";
import {
  MutationOptions,
  useMutation,
  UseMutationOptions,
  useQuery,
} from "@tanstack/react-query";
import { Bucket, Permissions } from "../types";
import { handleError } from "@/lib/utils";

export const useMultipartUploads = (bucketName: string) => {
  return useQuery({
    queryKey: ["multipart", bucketName],
    queryFn: () =>
      api.get<{ uploads: { key: string; uploadId: string; initiated: string }[] }>(
        `/multipart/${bucketName}`
      ),
    enabled: !!bucketName,
  });
};

export const useAbortMultipart = (
  bucketName: string,
  options?: MutationOptions<any, Error, { key: string; uploadId: string } | { all: true }>
) => {
  return useMutation({
    mutationFn: (v) =>
      api.delete(`/multipart/${bucketName}`, {
        params: "all" in v ? { all: true } : { key: v.key, uploadId: v.uploadId },
      }),
    ...options,
  });
};

export const useBucket = (id?: string | null) => {
  return useQuery({
    queryKey: ["bucket", id],
    queryFn: () => api.get<Bucket>("/v2/GetBucketInfo", { params: { id } }),
    enabled: !!id,
  });
};

export const useUpdateBucket = (
  id?: string | null,
  options?: UseMutationOptions<any, Error, any>
) => {
  return useMutation({
    mutationFn: (values: any) => {
      return api.post<any>("/v2/UpdateBucket", {
        params: { id },
        body: values,
      });
    },
    // These forms auto-save on change with no Save button, so a rejected
    // request has nothing to report it. Without this default the mutation
    // rejects silently and the form keeps showing a value the server never
    // accepted. `...options` is spread last so a caller can still override.
    onError: handleError,
    ...options,
  });
};

/** What we can say about a configured document after probing for it. */
export type ObjectPresence = "present" | "missing" | "unknown";

/**
 * Classifies the outcome of a HEAD probe.
 *
 * Only a 404 proves absence. Every other failure — most importantly the 500 a
 * bucket with no read+write key produces — means we could not tell, and the UI
 * must stay silent rather than accuse the user of a missing file.
 */
export const classifyObjectProbe = (
  isSuccess: boolean,
  error: unknown
): ObjectPresence => {
  if (isSuccess) return "present";
  if (error && (error as APIError).status === 404) return "missing";
  return "unknown";
};

/**
 * Probes whether `key` exists in `bucketName`. A GET to /browse/{bucket}/{key}
 * with no view/dl/thumb parameter performs a HeadObject server-side.
 *
 * `bucketName` is the bucket's GLOBAL ALIAS, not its id — every /browse route
 * resolves credentials via GetBucketInfo?globalAlias=.
 */
export const useObjectExists = (bucketName: string, key?: string | null) => {
  const query = useQuery({
    queryKey: ["object-exists", bucketName, key],
    queryFn: () => api.get(`/browse/${bucketName}/${encodeObjectPath(key!)}`),
    enabled: !!bucketName && !!key,
    // A 404 is the answer, not a transient failure worth retrying.
    retry: false,
  });

  return {
    presence: query.isLoading
      ? ("unknown" as ObjectPresence)
      : classifyObjectProbe(query.isSuccess, query.error),
    isLoading: query.isLoading,
  };
};

export const useAddAlias = (
  bucketId?: string | null,
  options?: UseMutationOptions<any, Error, string>
) => {
  return useMutation({
    mutationFn: (alias: string) => {
      return api.post("/v2/AddBucketAlias", {
        body: { bucketId, globalAlias: alias },
      });
    },
    ...options,
  });
};

export const useRemoveAlias = (
  bucketId?: string | null,
  options?: UseMutationOptions<any, Error, string>
) => {
  return useMutation({
    mutationFn: (alias: string) => {
      return api.post("/v2/RemoveBucketAlias", {
        body: { bucketId, globalAlias: alias },
      });
    },
    ...options,
  });
};

export const useAllowKey = (
  bucketId?: string | null,
  options?: MutationOptions<
    any,
    Error,
    { keyId: string; permissions: Permissions }[]
  >
) => {
  return useMutation({
    mutationFn: async (payload) => {
      const promises = payload.map(async (key) => {
        return api.post("/v2/AllowBucketKey", {
          body: {
            bucketId,
            accessKeyId: key.keyId,
            permissions: key.permissions,
          },
        });
      });
      const result = await Promise.all(promises);
      return result;
    },
    ...options,
  });
};

export const useDenyKey = (
  bucketId?: string | null,
  options?: MutationOptions<
    any,
    Error,
    { keyId: string; permissions: Permissions }
  >
) => {
  return useMutation({
    mutationFn: (payload) => {
      return api.post("/v2/DenyBucketKey", {
        body: {
          bucketId,
          accessKeyId: payload.keyId,
          permissions: payload.permissions,
        },
      });
    },
    ...options,
  });
};

export const useRemoveBucket = (
  options?: MutationOptions<any, Error, string>
) => {
  return useMutation({
    mutationFn: (id) => api.post("/v2/DeleteBucket", { params: { id } }),
    ...options,
  });
};
