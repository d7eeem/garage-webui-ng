import { Bucket } from "../types";
import { ArchiveIcon, ChartPie, ChartScatter, Globe } from "lucide-react";
import { readableBytes } from "@/lib/utils";
import { useConfig } from "@/hooks/useConfig";
import { getBucketWebsiteBaseUrl } from "@/lib/website";
import Button from "@/components/ui/button";

type Props = {
  data: Bucket & { aliases: string[] };
};

const BucketCard = ({ data }: Props) => {
  const { data: config } = useConfig();
  const websiteUrl = getBucketWebsiteBaseUrl(
    data.globalAliases?.[0] ?? "",
    config
  );

  return (
    <div className="card card-body p-6">
      <div className="grid grid-cols-2 items-start gap-4 p-2 pb-0">
        <div className="flex flex-row items-start gap-x-3 col-span-2">
          <ArchiveIcon size={28} className="shrink-0" />

          <div className="flex-1 min-w-0">
            <p className="text-xl font-medium truncate">
              {data.aliases?.join(", ")}
            </p>
            {data.websiteAccess &&
              (websiteUrl ? (
                <a
                  href={websiteUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="badge badge-ghost gap-1 mt-1 hover:link"
                >
                  <Globe size={16} />
                  Website
                </a>
              ) : (
                <span className="badge badge-ghost gap-1 mt-1">
                  <Globe size={16} />
                  Website
                </span>
              ))}
          </div>
        </div>

        <div>
          <p className="text-sm flex items-center gap-1">
            <ChartPie className="inline" size={16} />
            Usage
          </p>
          <p className="text-xl font-medium mt-1">
            {readableBytes(data.bytes)}
          </p>
        </div>

        <div>
          <p className="text-sm flex items-center gap-1">
            <ChartScatter className="inline" size={16} />
            Objects
          </p>
          <p className="text-xl font-medium mt-1">{data.objects}</p>
        </div>
      </div>

      <div className="flex flex-row justify-end gap-4">
        <Button href={`/buckets/${data.id}`}>Manage</Button>
        <Button color="primary" href={`/buckets/${data.id}?tab=browse`}>
          Browse
        </Button>
      </div>
    </div>
  );
};

export default BucketCard;
