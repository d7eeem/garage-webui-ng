import { describe, expect, it } from "vitest";
import {
  getBucketWebsiteBaseUrl,
  getBucketWebsiteObjectUrl,
  isWebsiteHostingConfigured,
} from "@/lib/website";
import type { Config, S3Web } from "@/types/garage";

// The live S3Web type requires bind_addr/root_domain/index, but the helper is
// defensively coded for a partially-present [s3_web] block. Build Config inputs
// from a Partial so edge cases (e.g. a missing bind_addr) can be exercised.
const mkConfig = (s3_web?: Partial<S3Web>): Config =>
  s3_web ? { s3_web: s3_web as S3Web } : {};

describe("isWebsiteHostingConfigured", () => {
  it("is false when config is undefined", () => {
    expect(isWebsiteHostingConfigured(undefined)).toBe(false);
  });

  it("is false when root_domain is empty", () => {
    expect(isWebsiteHostingConfigured(mkConfig({ root_domain: "" }))).toBe(
      false
    );
  });

  it("is false when root_domain is whitespace-only", () => {
    expect(isWebsiteHostingConfigured(mkConfig({ root_domain: "   " }))).toBe(
      false
    );
  });

  it("is true when a root_domain is configured", () => {
    expect(
      isWebsiteHostingConfigured(mkConfig({ root_domain: ".web.ex.com" }))
    ).toBe(true);
  });
});

describe("getBucketWebsiteBaseUrl", () => {
  it("returns null when there is no config", () => {
    expect(getBucketWebsiteBaseUrl("mybucket", undefined)).toBeNull();
  });

  it("returns null when root_domain is empty", () => {
    expect(
      getBucketWebsiteBaseUrl("mybucket", mkConfig({ root_domain: "" }))
    ).toBeNull();
  });

  it("returns null when the bucket name is empty", () => {
    expect(
      getBucketWebsiteBaseUrl("", mkConfig({ root_domain: ".web.ex.com" }))
    ).toBeNull();
  });

  it("builds the URL from root_domain and web port", () => {
    expect(
      getBucketWebsiteBaseUrl(
        "mybucket",
        mkConfig({ root_domain: ".web.ex.com", bind_addr: "[::]:3902" })
      )
    ).toBe("http://mybucket.web.ex.com:3902");
  });

  it("normalises a root_domain missing the leading dot", () => {
    expect(
      getBucketWebsiteBaseUrl(
        "mybucket",
        mkConfig({ root_domain: "web.ex.com", bind_addr: "[::]:3902" })
      )
    ).toBe("http://mybucket.web.ex.com:3902");
  });

  it("omits the port suffix for port 80", () => {
    expect(
      getBucketWebsiteBaseUrl(
        "mybucket",
        mkConfig({ root_domain: ".web.ex.com", bind_addr: "[::]:80" })
      )
    ).toBe("http://mybucket.web.ex.com");
  });

  it("omits the port suffix for port 443", () => {
    expect(
      getBucketWebsiteBaseUrl(
        "mybucket",
        mkConfig({ root_domain: ".web.ex.com", bind_addr: "0.0.0.0:443" })
      )
    ).toBe("http://mybucket.web.ex.com");
  });

  it("omits the port suffix when there is no bind_addr", () => {
    expect(
      getBucketWebsiteBaseUrl(
        "mybucket",
        mkConfig({ root_domain: ".web.ex.com" })
      )
    ).toBe("http://mybucket.web.ex.com");
  });
});

describe("getBucketWebsiteObjectUrl", () => {
  it("returns null when website hosting is not configured", () => {
    expect(
      getBucketWebsiteObjectUrl("mybucket", "index.html", undefined)
    ).toBeNull();
  });

  it("appends the object key to the base URL", () => {
    expect(
      getBucketWebsiteObjectUrl(
        "mybucket",
        "index.html",
        mkConfig({ root_domain: ".web.ex.com", bind_addr: "[::]:3902" })
      )
    ).toBe("http://mybucket.web.ex.com:3902/index.html");
  });

  it("strips a leading slash from the object key", () => {
    expect(
      getBucketWebsiteObjectUrl(
        "mybucket",
        "/leading",
        mkConfig({ root_domain: ".web.ex.com", bind_addr: "[::]:3902" })
      )
    ).toBe("http://mybucket.web.ex.com:3902/leading");
  });
});
