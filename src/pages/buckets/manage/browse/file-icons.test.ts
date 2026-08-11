import { describe, expect, it } from "vitest";
import {
  Clapperboard,
  FileArchive,
  FileAudio,
  FileCode,
  FileIcon,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileType,
  Presentation,
} from "lucide-react";
import { iconForObjectKey } from "./file-icons";

describe("iconForObjectKey", () => {
  it("picks FileImage for an image extension", () => {
    expect(iconForObjectKey("photo.jpg")).toBe(FileImage);
  });

  it("picks Clapperboard for a video extension", () => {
    expect(iconForObjectKey("a.mp4")).toBe(Clapperboard);
  });

  it("picks FileAudio for an audio extension", () => {
    expect(iconForObjectKey("song.mp3")).toBe(FileAudio);
  });

  it("picks FileText for a pdf", () => {
    expect(iconForObjectKey("report.pdf")).toBe(FileText);
  });

  it("picks FileSpreadsheet for a spreadsheet extension", () => {
    expect(iconForObjectKey("data.xlsx")).toBe(FileSpreadsheet);
  });

  it("picks Presentation for a presentation extension", () => {
    expect(iconForObjectKey("deck.pptx")).toBe(Presentation);
  });

  it("picks FileArchive for an archive extension", () => {
    expect(iconForObjectKey("bundle.zip")).toBe(FileArchive);
  });

  it("picks FileCode for a code extension", () => {
    expect(iconForObjectKey("script.ts")).toBe(FileCode);
  });

  it("picks FileType for a plain-text-ish extension", () => {
    expect(iconForObjectKey("notes.md")).toBe(FileType);
  });

  it("picks the default FileIcon for an unknown extension", () => {
    expect(iconForObjectKey("a.qqq")).toBe(FileIcon);
  });

  it("matches case-insensitively", () => {
    expect(iconForObjectKey("REPORT.PDF")).toBe(FileText);
    expect(iconForObjectKey("clip.MP4")).toBe(Clapperboard);
  });

  it("falls back to the default icon when there is no extension", () => {
    expect(iconForObjectKey("README")).toBe(FileIcon);
  });

  it("falls back to the default icon for a trailing dot", () => {
    expect(iconForObjectKey("weird.")).toBe(FileIcon);
  });

  it("falls back to the default icon for an unrecognized extension", () => {
    expect(iconForObjectKey("a.qqq")).toBe(FileIcon);
  });

  it("uses only the last segment of a dotted, multi-extension name", () => {
    // "a.b.tar.gz" should resolve on "gz", not "b" or "tar".
    expect(iconForObjectKey("a.b.tar.gz")).toBe(FileArchive);
  });

  it("uses only the extension of the final path segment, ignoring dots in earlier segments", () => {
    expect(iconForObjectKey("my.folder/file.png")).toBe(FileImage);
  });

  // .svg intentionally gets an image icon here even though the media viewer's
  // classifyMedia() refuses to preview SVG (it can carry <script> and the
  // backend won't serve it inline) — this map only picks a glyph, it does not
  // decide what can be rendered.
  it("picks FileImage for .svg even though the viewer will not preview it", () => {
    expect(iconForObjectKey("icon.svg")).toBe(FileImage);
  });
});
