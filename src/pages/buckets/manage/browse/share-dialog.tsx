import { createDisclosure } from "@/lib/disclosure";
import { Alert, Modal } from "react-daisyui";
import { useBucketContext } from "../context";
import { useConfig } from "@/hooks/useConfig";
import { useState } from "react";
import Input from "@/components/ui/input";
import Button from "@/components/ui/button";
import { Copy, FileWarningIcon } from "lucide-react";
import { copyToClipboard, handleError } from "@/lib/utils";
import { getBucketWebsiteObjectUrl } from "@/lib/website";
import Checkbox from "@/components/ui/checkbox";
import { useShareLink } from "./hooks";

export const shareDialog = createDisclosure<{ key: string; prefix: string }>();

const EXPIRY_OPTIONS = [
  { label: "15 minutes", value: 900 },
  { label: "1 hour", value: 3600 },
  { label: "24 hours", value: 86400 },
  { label: "7 days", value: 604800 },
];

const ShareDialog = () => {
  const { isOpen, data, dialogRef } = shareDialog.use();
  const { bucket, bucketName } = useBucketContext();
  const { data: config } = useConfig();
  const [expires, setExpires] = useState(EXPIRY_OPTIONS[1].value);
  const [linkResult, setLinkResult] = useState<{
    key: string;
    url: string;
  } | null>(null);
  const shareLink = useShareLink(bucketName);

  const objectKey = (data?.prefix ?? "") + (data?.key ?? "");
  const websiteUrl = getBucketWebsiteObjectUrl(bucketName, objectKey, config);

  const onGenerateLink = () => {
    shareLink.mutate(
      { key: objectKey, expires },
      {
        onSuccess: (res) => setLinkResult({ key: objectKey, url: res.url }),
        onError: handleError,
      }
    );
  };

  return (
    <Modal ref={dialogRef} open={isOpen} backdrop>
      <Modal.Header className="truncate">Share {data?.key || ""}</Modal.Header>
      <Modal.Body>
        {config?.sharing && (
          <div className="flex flex-col gap-2 mb-4 pb-4 border-b border-base-content/10">
            <p className="label label-text py-0">Private link (expires)</p>

            <div className="flex flex-row flex-wrap gap-2">
              {EXPIRY_OPTIONS.map((opt) => (
                <Checkbox
                  key={opt.value}
                  label={opt.label}
                  checked={expires === opt.value}
                  onChange={() => setExpires(opt.value)}
                />
              ))}
            </div>

            <Button
              onClick={onGenerateLink}
              disabled={shareLink.isPending}
              className="self-start"
            >
              {shareLink.isPending ? "Generating…" : "Generate link"}
            </Button>

            {linkResult && linkResult.key === objectKey && (
              <div className="relative mt-2">
                <Input
                  value={linkResult.url}
                  className="w-full pr-12"
                  onFocus={(e) => e.target.select()}
                  readOnly
                />
                <Button
                  icon={Copy}
                  onClick={() => copyToClipboard(linkResult.url)}
                  className="absolute top-0 right-0"
                  color="ghost"
                />
              </div>
            )}
          </div>
        )}

        {!bucket.websiteAccess && (
          <Alert className="mb-4 items-start text-sm">
            <FileWarningIcon className="mt-1" />
            Sharing is only available for buckets with enabled website access.
          </Alert>
        )}
        {websiteUrl && (
          <div className="relative mt-2">
            <Input
              value={websiteUrl}
              className="w-full pr-12"
              onFocus={(e) => e.target.select()}
              readOnly
            />
            <Button
              icon={Copy}
              onClick={() => copyToClipboard(websiteUrl)}
              className="absolute top-0 right-0"
              color="ghost"
            />
          </div>
        )}
      </Modal.Body>
      <Modal.Actions>
        <Button onClick={() => shareDialog.close()}>Close</Button>
      </Modal.Actions>
    </Modal>
  );
};

export default ShareDialog;
