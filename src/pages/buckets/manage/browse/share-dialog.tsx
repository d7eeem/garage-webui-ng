import { createDisclosure } from "@/lib/disclosure";
import { Alert, Modal } from "react-daisyui";
import { useBucketContext } from "../context";
import { useConfig } from "@/hooks/useConfig";
import { useState } from "react";
import Input from "@/components/ui/input";
import Button from "@/components/ui/button";
import { Copy, ExternalLink, FileWarningIcon } from "lucide-react";
import { copyToClipboard, handleError } from "@/lib/utils";
import { getPublicAccess } from "@/lib/website";
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
  const publicAccess = getPublicAccess(
    bucket.websiteAccess,
    bucketName,
    objectKey,
    config
  );

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
        {config?.sharing ? (
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
        ) : (
          <Alert className="mb-4 items-start text-sm">
            <FileWarningIcon className="mt-1 shrink-0" />
            <span>
              Expiring private links are not enabled. Set{" "}
              <code>S3_PUBLIC_ENDPOINT_URL</code> to the S3 API address your
              link recipients can reach (for example{" "}
              <code>https://s3.example.com</code>) and restart the app.
            </span>
          </Alert>
        )}

        {publicAccess.state === "private" && (
          <Alert className="mb-4 items-start text-sm">
            <FileWarningIcon className="mt-1 shrink-0" />
            <span>
              This bucket has no public read access, so it has no public URL.
              {config?.sharing ? " Private links above still work." : ""}
            </span>
          </Alert>
        )}
        {publicAccess.state === "public-no-url" && (
          <Alert className="mb-4 items-start text-sm">
            <FileWarningIcon className="mt-1 shrink-0" />
            <span>
              Public read is enabled for this bucket, but no public URL can be
              built yet. Set <code>S3_WEB_PUBLIC_URL</code> (or Garage's{" "}
              <code>[s3_web] root_domain</code>) and restart the app.
            </span>
          </Alert>
        )}
        {publicAccess.state === "public" && (
          <div className="mt-2">
            <p className="label label-text py-0">Public link (no expiry)</p>
            <div className="relative mt-2">
              <Input
                value={publicAccess.url}
                className="w-full pr-20"
                onFocus={(e) => e.target.select()}
                readOnly
              />
              <div className="absolute top-0 right-0 flex flex-row">
                <Button
                  href={publicAccess.url}
                  target="_blank"
                  rel="noreferrer"
                  icon={ExternalLink}
                  color="ghost"
                />
                <Button
                  icon={Copy}
                  onClick={() => copyToClipboard(publicAccess.url)}
                  color="ghost"
                />
              </div>
            </div>
            {publicAccess.url.startsWith("http://") &&
              window.location.protocol === "https:" && (
                <p className="text-xs text-warning mt-1">
                  This link is plain HTTP while the console is served over
                  HTTPS. Set <code>S3_WEB_PUBLIC_URL</code> to your website
                  endpoint's public HTTPS address.
                </p>
              )}
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
