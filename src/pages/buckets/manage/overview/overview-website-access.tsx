import { DeepPartial, useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { websiteConfigSchema, WebsiteConfigSchema } from "../schema";
import { useEffect } from "react";
import { useDebounce } from "@/hooks/useDebounce";
import { useUpdateBucket } from "../hooks";
import { useConfig } from "@/hooks/useConfig";
import { CircleXIcon, Copy, Info, LinkIcon } from "lucide-react";
import { Alert } from "react-daisyui";
import Button from "@/components/ui/button";
import { InputField } from "@/components/ui/input";
import { ToggleField } from "@/components/ui/toggle";
import { useBucketContext } from "../context";
import { useAuth } from "@/hooks/useAuth";
import { copyToClipboard } from "@/lib/utils";
import {
  getBucketWebsiteBaseUrl,
  isWebsiteHostingConfigured,
} from "@/lib/website";

const WebsiteAccessSection = () => {
  const { canWrite } = useAuth();
  const { bucket: data, bucketName } = useBucketContext();
  const { data: config } = useConfig();
  const form = useForm<WebsiteConfigSchema>({
    resolver: zodResolver(websiteConfigSchema),
  });
  const isEnabled = useWatch({ control: form.control, name: "websiteAccess" });

  const websiteUrl = getBucketWebsiteBaseUrl(bucketName, config);

  const updateMutation = useUpdateBucket(data?.id);

  const onChange = useDebounce((values: DeepPartial<WebsiteConfigSchema>) => {
    const data = {
      enabled: values.websiteAccess,
      indexDocument: values.websiteAccess
        ? values.websiteConfig?.indexDocument
        : undefined,
      errorDocument: values.websiteAccess
        ? values.websiteConfig?.errorDocument
        : undefined,
    };

    updateMutation.mutate({
      websiteAccess: data,
    });
  });

  useEffect(() => {
    form.reset({
      websiteAccess: data?.websiteAccess,
      websiteConfig: {
        indexDocument: data?.websiteConfig?.indexDocument || "index.html",
        errorDocument: data?.websiteConfig?.errorDocument || "error/400.html",
      },
    });

    const { unsubscribe } = form.watch((values) => onChange(values));
    return unsubscribe;
  }, [data]);

  return (
    <div className="mt-8">
      <div className="flex flex-row gap-2">
        <p className="label label-text py-0 grow-0">Website Access</p>
        <Button
          href="https://garagehq.deuxfleurs.fr/documentation/cookbook/exposing-websites"
          target="_blank"
          size="sm"
          shape="circle"
          color="ghost"
        >
          <Info size={16} />
        </Button>
      </div>

      <ToggleField
        form={form}
        name="websiteAccess"
        label="Enabled"
        disabled={!canWrite}
      />

      {isEnabled && (
        <>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <InputField
              form={form}
              name="websiteConfig.indexDocument"
              title="Index Document"
              disabled={!canWrite}
            />
            <InputField
              form={form}
              name="websiteConfig.errorDocument"
              title="Error Document"
              disabled={!canWrite}
            />
          </div>

          {websiteUrl ? (
            <div className="mt-4 alert flex flex-row flex-wrap items-center text-sm gap-x-2 gap-y-1">
              <a
                href={websiteUrl}
                className="inline-flex items-center flex-row gap-2 font-medium hover:link"
                target="_blank"
                rel="noreferrer"
              >
                <LinkIcon size={14} />
                {websiteUrl}
              </a>
              <Button
                icon={Copy}
                onClick={() => copyToClipboard(websiteUrl)}
                size="sm"
                color="ghost"
              />
            </div>
          ) : !isWebsiteHostingConfigured(config) ? (
            <Alert
              status="warning"
              icon={<CircleXIcon />}
              className="mt-4 items-start text-sm"
            >
              <span>
                Garage has no web endpoint configured, so this bucket has no
                public website URL. Add an <code>[s3_web]</code> block with{" "}
                <code>bind_addr</code> and <code>root_domain</code> to{" "}
                <code>garage.toml</code>.
              </span>
            </Alert>
          ) : null}
        </>
      )}
    </div>
  );
};

export default WebsiteAccessSection;
