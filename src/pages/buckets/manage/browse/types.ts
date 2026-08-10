export type UseBrowserObjectOptions = Partial<{
  prefix: string;
  limit: number;
}>;

export type GetObjectsResult = {
  prefixes: string[];
  objects: Object[];
  prefix: string;
  nextToken: string | null;
};

export type Object = {
  objectKey: string;
  lastModified: Date;
  size: number;
  url: string;
};

export type PutObjectPayload = {
  key: string;
  file: File | null;
};

export type BulkDeleteResult = {
  deleted: number;
  errors: { key: string; message: string }[];
};

export type UploadStatus = "queued" | "uploading" | "done" | "error" | "canceled";

export type UploadItem = {
  /** Stable identity for React keys and cancel lookups. */
  id: string;
  /** Full object key, i.e. prefix + file name. */
  key: string;
  /** Display name — the file name without the prefix. */
  name: string;
  bucket: string;
  size: number;
  loaded: number;
  status: UploadStatus;
  /** Populated only when status === "error". Server text, or a diagnostic. */
  error?: string;
};
