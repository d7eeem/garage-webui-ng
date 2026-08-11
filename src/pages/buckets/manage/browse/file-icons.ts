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
  type LucideIcon,
} from "lucide-react";

// Extension groups, each mapped to a single generic icon below. Extension-
// driven on purpose, not content-type-driven: the library the previous
// row-preview component used for that has known gaps (.ico, .avif, .heic),
// and going through it is what made that component fragile. Do not import
// that library in this file.
const ICON_GROUPS: [LucideIcon, string[]][] = [
  [
    FileImage,
    ["png", "jpg", "jpeg", "gif", "webp", "avif", "bmp", "ico", "svg", "tif", "tiff", "heic"],
  ],
  [
    Clapperboard,
    ["mp4", "webm", "mkv", "mov", "avi", "m4v", "mpg", "mpeg", "wmv"],
  ],
  [FileAudio, ["mp3", "wav", "ogg", "oga", "m4a", "flac", "aac", "opus"]],
  [FileText, ["pdf"]],
  [FileSpreadsheet, ["xlsx", "xls", "csv", "tsv", "ods"]],
  [Presentation, ["ppt", "pptx", "odp"]],
  [FileArchive, ["zip", "rar", "7z", "iso", "tar", "gz", "bz2", "xz"]],
  [
    FileCode,
    [
      "js",
      "jsx",
      "ts",
      "tsx",
      "json",
      "go",
      "py",
      "rb",
      "sh",
      "bash",
      "yml",
      "yaml",
      "toml",
      "html",
      "css",
      "scss",
      "sql",
    ],
  ],
  [FileType, ["txt", "md", "markdown", "rst", "log", "doc", "docx", "odt", "rtf"]],
];

const EXTENSION_ICON: Record<string, LucideIcon> = {};
for (const [icon, extensions] of ICON_GROUPS) {
  for (const ext of extensions) {
    EXTENSION_ICON[ext] = icon;
  }
}

/**
 * Pick a generic icon for an object, from its file extension.
 *
 * Deliberately NOT a thumbnail: the previous implementation fetched the object
 * itself to draw a 20px row icon, which cost the full file for every image the
 * backend could not thumbnail (it decodes only gif/jpeg/png), and broke
 * outright for .svg once object bodies started being served as attachments.
 *
 * This is also NOT classifyMedia() in media-viewer.tsx. That mirrors a backend
 * security allowlist and decides what may be RENDERED; this only decides which
 * glyph to draw, so it may cover types the viewer refuses. Keep them separate.
 */
export function iconForObjectKey(objectKey: string): LucideIcon {
  const dotIdx = objectKey.lastIndexOf(".");
  if (dotIdx < 0 || dotIdx === objectKey.length - 1) {
    return FileIcon;
  }

  const ext = objectKey.substring(dotIdx + 1).toLowerCase();
  return EXTENSION_ICON[ext] ?? FileIcon;
}
