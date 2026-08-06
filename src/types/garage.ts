export type Config = {
  s3_api?: S3API;
  s3_web?: S3Web;
  sharing?: boolean;
};

export type S3API = {
  s3_region: string;
  root_domain: string;
};

export type S3Web = {
  bind_addr: string;
  root_domain: string;
  index: string;
  /**
   * Operator-declared public base URL (S3_WEB_PUBLIC_URL). Empty when unset.
   * May contain the literal token "{bucket}" for vhost-style addressing.
   */
  public_url?: string;
};
