import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import MediaViewer, { classifyMedia, mediaViewer } from "./media-viewer";

describe("classifyMedia", () => {
  it("classifies previewable extensions", () => {
    expect(classifyMedia("photo.png")).toBe("image");
    expect(classifyMedia("clip.mp4")).toBe("video");
    expect(classifyMedia("song.mp3")).toBe("audio");
    expect(classifyMedia("doc.pdf")).toBe("pdf");
    expect(classifyMedia("notes.txt")).toBe("text");
  });

  it("returns null for .svg — a security boundary, not a preference", () => {
    expect(classifyMedia("icon.svg")).toBeNull();
  });

  it("returns null for other non-previewable types", () => {
    expect(classifyMedia("archive.zip")).toBeNull();
    expect(classifyMedia("report.docx")).toBeNull();
    expect(classifyMedia("no-extension")).toBeNull();
  });
});

describe("MediaViewer", () => {
  beforeEach(() => {
    act(() => {
      mediaViewer.close();
    });
  });

  it("renders an <img> for an image item when open", () => {
    render(<MediaViewer />);

    act(() => {
      mediaViewer.open({
        items: [{ objectKey: "photo.png", url: "/photo.png" }],
        index: 0,
      });
    });

    const img = screen.getByRole("img", { name: "photo.png" });
    expect(img).toBeInTheDocument();
    expect(img.getAttribute("src")).toContain("/photo.png?view=1");
  });

  it("shows the download fallback after the media element fires onError", () => {
    render(<MediaViewer />);

    act(() => {
      mediaViewer.open({
        items: [{ objectKey: "photo.png", url: "/photo.png" }],
        index: 0,
      });
    });

    const img = screen.getByRole("img", { name: "photo.png" });
    fireEvent.error(img);

    expect(
      screen.getByText("Preview unavailable. You can still download the file.")
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download" })).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: "photo.png" })).toBeNull();
  });

  it("hides prev/next controls for a single item", () => {
    render(<MediaViewer />);

    act(() => {
      mediaViewer.open({
        items: [{ objectKey: "photo.png", url: "/photo.png" }],
        index: 0,
      });
    });

    expect(screen.queryByRole("button", { name: "Previous" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
  });

  it("shows prev/next controls for two or more items", () => {
    render(<MediaViewer />);

    act(() => {
      mediaViewer.open({
        items: [
          { objectKey: "a.png", url: "/a.png" },
          { objectKey: "b.png", url: "/b.png" },
        ],
        index: 0,
      });
    });

    expect(screen.getByRole("button", { name: "Previous" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next" })).toBeInTheDocument();
    expect(screen.getByText("1 of 2")).toBeInTheDocument();
  });
});
