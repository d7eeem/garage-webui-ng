import { afterEach, describe, expect, it } from "vitest";
import { readBasePath } from "@/lib/consts";

describe("readBasePath", () => {
  afterEach(() => {
    document.head.innerHTML = "";
  });

  it("returns the base path from the meta tag", () => {
    document.head.innerHTML =
      '<meta name="app-base-path" content="/garage" />';

    expect(readBasePath()).toBe("/garage");
  });

  it("treats the unsubstituted placeholder as no base path", () => {
    // A direct request for /index.html is served without the Go server's
    // %BASE_PATH% substitution, so the literal placeholder can reach the
    // browser. It must not be treated as a real path.
    document.head.innerHTML =
      '<meta name="app-base-path" content="%BASE_PATH%" />';

    expect(readBasePath()).toBe("");
  });

  it("returns an empty string when the meta tag is missing", () => {
    document.head.innerHTML = "";

    expect(readBasePath()).toBe("");
  });
});
