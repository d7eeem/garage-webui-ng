import { TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import { readableBytes, handleError } from "@/lib/utils";
import Button from "@/components/ui/button";
import { useAbortMultipart } from "../hooks";
import { useBucketContext } from "../context";

const MultipartUploadsSection = () => {
  const { bucket: data, bucketName, refetch } = useBucketContext();

  const abortMutation = useAbortMultipart(bucketName, {
    onSuccess: () => {
      toast.success("Orphaned multipart uploads aborted!");
      refetch();
    },
    onError: handleError,
  });

  if (!data?.unfinishedMultipartUploads) {
    return null;
  }

  const onAbortAll = () => {
    if (
      window.confirm(
        "This aborts ALL in-progress multipart uploads for this bucket. " +
          "This cannot be undone. Continue?"
      )
    ) {
      abortMutation.mutate({ all: true });
    }
  };

  return (
    <div className="mt-4 alert flex flex-row flex-wrap items-center gap-3 text-sm">
      <TriangleAlert size={18} className="text-warning" />
      <div className="flex-1">
        <p>
          {data.unfinishedMultipartUploads} orphaned multipart upload
          {data.unfinishedMultipartUploads === 1 ? "" : "s"} using{" "}
          {readableBytes(data.unfinishedMultipartUploadBytes)}
        </p>
      </div>
      <Button
        size="sm"
        color="error"
        loading={abortMutation.isPending}
        onClick={onAbortAll}
      >
        Abort all
      </Button>
    </div>
  );
};

export default MultipartUploadsSection;
