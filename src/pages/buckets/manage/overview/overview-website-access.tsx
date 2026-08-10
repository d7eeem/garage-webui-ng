import { DeepPartial, useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { websiteConfigSchema, WebsiteConfigSchema } from "../schema";
import { useEffect, useState } from "react";
import { useDebounce } from "@/hooks/useDebounce";
import { useObjectExists, useUpdateBucket } from "../hooks";
import { useConfig } from "@/hooks/useConfig";
import { CircleXIcon, Copy, Info, LinkIcon } from "lucide-react";
import { Alert } from "react-daisyui";
import Button from "@/components/ui/button";
import { InputField } from "@/components/ui/input";
import Toggle from "@/components/ui/toggle";
import { useBucketContext } from "../context";
import { useAuth } from "@/hooks/useAuth";
import { copyToClipboard } from "@/lib/utils";
import {
  getBucketWebsiteBaseUrl,
  isWebsiteHostingConfigured,
} from "@/lib/website";
import SaveStatus from "./save-status";
import PublicAccessConfirm from "./public-access-confirm";

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
  const [confirmOpen, setConfirmOpen] = useState(false);

  // Probe the PERSISTED config, not the live form values — the form value
  // changes on every keystroke, which would fire a request per character.
  // `data` only changes when a save round-trips.
  const indexDoc = data?.websiteConfig?.indexDocument;
  const errorDoc = data?.websiteConfig?.errorDocument;
  const indexPresence = useObjectExists(
    bucketName,
    data?.websiteAccess ? indexDoc : null
  );
  const errorPresence = useObjectExists(
    bucketName,
    data?.websiteAccess ? errorDoc : null
  );

  // Debounced auto-save for the index/error document fields only.
  // `websiteAccess` is driven explicitly (see the toggle's onChange below) —
  // enabling requires confirmation, so it must never ride this path.
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

    // `name` is the field that changed. A `websiteAccess` change is handled
    // explicitly by the toggle's onChange below (it needs a confirm step on
    // enable, and must save immediately rather than after a 500ms debounce
    // either way) — skip it here so it never double-saves or bypasses the
    // confirmation.
    const { unsubscribe } = form.watch((values, { name }) => {
      if (name === "websiteAccess") return;
      onChange(values);
    });
    return unsubscribe;
  }, [data]);

  const onToggleWebsiteAccess = (next: boolean) => {
    if (next) {
      // Enabling makes every object anonymously readable — require an
      // explicit confirmation before touching form state or Garage. The
      // toggle stays visually OFF (form value untouched) until confirmed.
      setConfirmOpen(true);
      return;
    }

    // Disabling is the safe direction: apply and save immediately, no
    // confirmation, matching the plan's "disabling should be easy" design.
    form.setValue("websiteAccess", false, { shouldDirty: true });
    updateMutation.mutate({ websiteAccess: { enabled: false } });
  };

  const onConfirmEnable = () => {
    form.setValue("websiteAccess", true, { shouldDirty: true });
    const values = form.getValues();
    updateMutation.mutate({
      websiteAccess: {
        enabled: true,
        indexDocument: values.websiteConfig?.indexDocument,
        errorDocument: values.websiteConfig?.errorDocument,
      },
    });
    setConfirmOpen(false);
  };

  const onCancelEnable = () => {
    setConfirmOpen(false);
  };

  return (
    <div className="mt-8">
      <div className="flex flex-row items-center gap-2">
        <p className="label label-text py-0 grow-0">
          Public read (website hosting)
        </p>
        <Button
          href="https://garagehq.deuxfleurs.fr/documentation/cookbook/exposing-websites"
          target="_blank"
          size="sm"
          shape="circle"
          color="ghost"
        >
          <Info size={16} />
        </Button>
        <SaveStatus
          isPending={updateMutation.isPending}
          isSuccess={updateMutation.isSuccess}
          isError={updateMutation.isError}
        />
      </div>

      <Toggle
        label="Enabled"
        disabled={!canWrite}
        color={isEnabled ? "primary" : undefined}
        checked={!!isEnabled}
        onChange={(e) => onToggleWebsiteAccess(e.target.checked)}
      />

      <PublicAccessConfirm
        bucketName={bucketName}
        isOpen={confirmOpen}
        onCancel={onCancelEnable}
        onConfirm={onConfirmEnable}
      />

      {isEnabled && (
        <>
          <p className="text-xs text-base-content/60 mt-1">
            Anyone who can reach the Garage website endpoint can retrieve
            objects in this bucket without signing in. Uploads and deletions
            still require credentials.
          </p>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <InputField
                form={form}
                name="websiteConfig.indexDocument"
                title="Index Document"
                disabled={!canWrite}
              />
              {indexPresence.presence === "missing" ? (
                <p className="text-xs text-warning mt-1">
                  Not found in this bucket — visitors will get an error until
                  you upload it.
                </p>
              ) : null}
            </div>
            <div>
              <InputField
                form={form}
                name="websiteConfig.errorDocument"
                title="Error Document"
                disabled={!canWrite}
              />
              {errorPresence.presence === "missing" ? (
                <p className="text-xs text-warning mt-1">
                  Not found in this bucket — visitors will get an error until
                  you upload it.
                </p>
              ) : null}
            </div>
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
