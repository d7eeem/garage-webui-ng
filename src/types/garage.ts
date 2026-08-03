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
};
