import { Alert, Loading, Table } from "react-daisyui";
import { useBrowseObjects } from "./hooks";
import { dayjs, readableBytes } from "@/lib/utils";
import { Object } from "./types";
import { API_URL } from "@/lib/api";
import { ChevronDown, ChevronUp, CircleXIcon, Folder } from "lucide-react";
import { useBucketContext } from "../context";
import ObjectActions from "./object-actions";
import GotoTopButton from "@/components/ui/goto-top-btn";
import Button from "@/components/ui/button";
import Checkbox from "@/components/ui/checkbox";
import { Dispatch, ReactNode, SetStateAction, useMemo, useState } from "react";
import { useConfig } from "@/hooks/useConfig";
import { getPublicAccess } from "@/lib/website";
import { classifyMedia, mediaViewer } from "./media-viewer";
import { iconForObjectKey } from "./file-icons";
import {
  DEFAULT_SORT,
  SortColumn,
  SortState,
  sortObjects,
  sortPrefixes,
} from "./sorting";

type Props = {
  prefix?: string;
  onPrefixChange?: (prefix: string) => void;
  selected: Set<string>;
  setSelected: Dispatch<SetStateAction<Set<string>>>;
};

const ObjectList = ({
  prefix,
  onPrefixChange,
  selected,
  setSelected,
}: Props) => {
  const { bucket, bucketName } = useBucketContext();
  const { data: config } = useConfig();
  const {
    data,
    error,
    isLoading,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useBrowseObjects(bucketName, { prefix, limit: 1000 });

  const [sort, setSort] = useState<SortState>(DEFAULT_SORT);

  const pages = data?.pages ?? [];
  const prefixes = pages.flatMap((page) => page.prefixes);
  const objects = pages.flatMap((page) => page.objects);
  const currentPrefix = pages[0]?.prefix ?? "";

  // The sort is client-side over whatever pages have been loaded so far — S3's
  // ListObjectsV2 has no server-side sort. The hint rendered below (when
  // hasNextPage is true and the sort isn't the default) is what keeps that
  // honest for the user.
  const sortedObjects = useMemo(() => sortObjects(objects, sort), [objects, sort]);
  const sortedPrefixes = useMemo(
    () => sortPrefixes(prefixes, sort.column === "name" ? sort.direction : "asc"),
    [prefixes, sort]
  );

  const toggleSort = (column: SortColumn) => {
    setSort((prev) =>
      prev.column === column
        ? { column, direction: prev.direction === "asc" ? "desc" : "asc" }
        : { column, direction: "asc" }
    );
  };

  const previewable = useMemo(
    () =>
      sortedObjects
        .filter((object) => classifyMedia(object.objectKey) !== null)
        .map((object) => ({ objectKey: object.objectKey, url: object.url })),
    [sortedObjects]
  );

  const isPartialSort =
    hasNextPage &&
    (sort.column !== DEFAULT_SORT.column || sort.direction !== DEFAULT_SORT.direction);

  const allLoadedKeys = sortedObjects.map(
    (object) => currentPrefix + object.objectKey
  );
  const allLoadedSelected =
    allLoadedKeys.length > 0 && allLoadedKeys.every((key) => selected.has(key));

  const toggleSelectAll = () => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (allLoadedSelected) {
        allLoadedKeys.forEach((key) => next.delete(key));
      } else {
        allLoadedKeys.forEach((key) => next.add(key));
      }
      return next;
    });
  };

  const toggleSelectOne = (key: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  const onObjectClick = (object: Object) => {
    if (classifyMedia(object.objectKey) !== null) {
      const index = previewable.findIndex(
        (item) => item.objectKey === object.objectKey
      );
      mediaViewer.open({ items: previewable, index: Math.max(0, index) });
      return;
    }

    // Not previewable (e.g. .zip, .docx, .svg): keep today's behaviour
    // exactly. The backend serves it as an attachment, so this is the
    // download path.
    // object.url arrives percent-encoded from the API; do not re-encode.
    window.open(API_URL + object.url + "?view=1", "_blank");
  };

  return (
    <div className="overflow-x-auto min-h-[400px]">
      {isPartialSort && (
        <p className="text-xs text-base-content/60 px-2 pb-1">
          Sorted across the objects loaded so far — load more to sort the
          rest.
        </p>
      )}

      <Table>
        <Table.Head>
          <span>
            <Checkbox
              checked={allLoadedSelected}
              onChange={toggleSelectAll}
              aria-label="Select all loaded objects"
            />
          </span>
          <SortableHeader column="name" sort={sort} onSort={toggleSort}>
            Name
          </SortableHeader>
          <SortableHeader column="size" sort={sort} onSort={toggleSort}>
            Size
          </SortableHeader>
          <SortableHeader column="lastModified" sort={sort} onSort={toggleSort}>
            Last Modified
          </SortableHeader>
          {/* The actions column. `Table.Head` renders one <th> per child, so
              this must exist or the table declares 4 columns while every row
              renders 5 — the browser then has no header to size the actions
              column against, and it is pushed out of the container.
              Visually empty, but named for screen readers. */}
          <span className="sr-only">Actions</span>
        </Table.Head>

        <Table.Body>
          {isLoading ? (
            <tr>
              <td colSpan={5}>
                <div className="h-[320px] flex items-center justify-center">
                  <Loading />
                </div>
              </td>
            </tr>
          ) : error ? (
            <tr>
              <td colSpan={5}>
                <Alert status="error" icon={<CircleXIcon />}>
                  <span>{error.message}</span>
                </Alert>
              </td>
            </tr>
          ) : !sortedPrefixes.length && !sortedObjects.length ? (
            <tr>
              <td className="text-center py-16" colSpan={5}>
                No objects
              </td>
            </tr>
          ) : null}

          {sortedPrefixes.map((prefix) => (
            <tr
              key={prefix}
              className="hover:bg-neutral/60 hover:text-neutral-content group"
            >
              <td />
              <td
                className="cursor-pointer"
                role="button"
                onClick={() => onPrefixChange?.(prefix)}
              >
                <span className="flex items-center gap-2 font-normal">
                  <Folder size={20} className="text-primary" />
                  {prefix
                    .substring(0, prefix.lastIndexOf("/"))
                    .split("/")
                    .pop()}
                </span>
              </td>
              <td colSpan={2} />
              <ObjectActions object={{ objectKey: prefix, url: "" }} />
            </tr>
          ))}

          {sortedObjects.map((object) => {
            const extIdx = object.objectKey.lastIndexOf(".");
            const filename =
              extIdx >= 0
                ? object.objectKey.substring(0, extIdx)
                : object.objectKey;
            const ext = extIdx >= 0 ? object.objectKey.substring(extIdx) : null;
            const fullKey = currentPrefix + object.objectKey;
            const isPublic =
              getPublicAccess(bucket.websiteAccess, bucketName, fullKey, config)
                .state === "public";

            return (
              <tr
                key={object.objectKey}
                className="hover:bg-neutral/60 hover:text-neutral-content group"
              >
                <td>
                  <Checkbox
                    checked={selected.has(fullKey)}
                    onChange={() => toggleSelectOne(fullKey)}
                    aria-label={`Select ${object.objectKey}`}
                  />
                </td>
                <td
                  className="cursor-pointer max-w-0 w-full"
                  role="button"
                  onClick={() => onObjectClick(object)}
                >
                  <span className="flex items-center font-normal w-full min-w-0">
                    <FilePreview objectKey={object.objectKey} />
                    <span className="truncate min-w-0">{filename}</span>
                    {ext && (
                      <span className="text-base-content/60 shrink-0">{ext}</span>
                    )}
                    {isPublic && (
                      <span className="badge badge-ghost badge-sm ml-2 shrink-0">
                        Public
                      </span>
                    )}
                  </span>
                </td>
                <td className="whitespace-nowrap">
                  {readableBytes(object.size)}
                </td>
                <td className="whitespace-nowrap">
                  {dayjs(object.lastModified).fromNow()}
                </td>
                <ObjectActions prefix={currentPrefix} object={object} />
              </tr>
            );
          })}

          {hasNextPage ? (
            <tr>
              <td colSpan={5} className="text-center">
                <Button
                  color="ghost"
                  onClick={() => fetchNextPage()}
                  disabled={isFetchingNextPage}
                >
                  {isFetchingNextPage ? "Loading…" : "Load more"}
                </Button>
              </td>
            </tr>
          ) : null}
        </Table.Body>
      </Table>

      <GotoTopButton />
    </div>
  );
};

type SortableHeaderProps = {
  column: SortColumn;
  sort: SortState;
  onSort: (column: SortColumn) => void;
  children: ReactNode;
};

const COLUMN_LABEL: Record<SortColumn, string> = {
  name: "name",
  size: "size",
  lastModified: "last modified",
};

/**
 * A sortable column header. `Table.Head` renders one <th> per direct child,
 * so this is itself a single child slot — see the comment above the actions
 * column in ObjectList. `Table.Head` also owns the <th> it wraps this in, so
 * there is no prop path to put `aria-sort` on the <th> itself; it goes on
 * this component's root <span> instead.
 */
const SortableHeader = ({ column, sort, onSort, children }: SortableHeaderProps) => {
  const isActive = sort.column === column;

  return (
    <span
      aria-sort={
        isActive ? (sort.direction === "asc" ? "ascending" : "descending") : undefined
      }
    >
      <button
        type="button"
        className="inline-flex items-center gap-1 hover:text-base-content"
        onClick={() => onSort(column)}
        aria-label={`Sort by ${COLUMN_LABEL[column]}`}
      >
        {children}
        {isActive ? (
          sort.direction === "asc" ? (
            <ChevronUp size={14} />
          ) : (
            <ChevronDown size={14} />
          )
        ) : null}
      </button>
    </span>
  );
};

type FilePreviewProps = {
  objectKey: string;
};

const FilePreview = ({ objectKey }: FilePreviewProps) => {
  const Icon = iconForObjectKey(objectKey);
  return (
    <Icon
      size={20}
      className="text-base-content/60 group-hover:text-neutral-content/80 mr-2 shrink-0"
    />
  );
};

export default ObjectList;
