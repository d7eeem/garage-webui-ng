import { useConfig } from "@/hooks/useConfig";
import { Card } from "react-daisyui";
import { useUpdateCheck } from "./hooks";

/**
 * Build identity of the running app, plus an opt-in check for a newer
 * release. The check itself is server-side and cached (backend/router/update.go)
 * — this component only renders whatever it returns; it never calls GitHub
 * directly.
 */
const AboutTab = () => {
  const { data: config } = useConfig();
  const { data: update } = useUpdateCheck();

  return (
    <Card className="bg-base-100 max-w-xl" bordered>
      <Card.Body>
        <Card.Title tag="h2">About</Card.Title>

        <div className="flex flex-row items-start gap-3 text-left text-sm">
          <div className="shrink-0 w-1/3 max-w-[200px]">
            <p className="text-base-content/80">Garage WebUI-NG</p>
          </div>
          <div className="flex-1 truncate">
            <p className="truncate">{config?.version ?? "unknown"}</p>
          </div>
        </div>

        {update?.updateAvailable && update.latest ? (
          <p className="text-sm mt-2">
            Update available: {update.latest}
            {update.url ? (
              <>
                {" — "}
                <a
                  href={update.url}
                  target="_blank"
                  rel="noreferrer"
                  className="link"
                >
                  view release
                </a>
              </>
            ) : null}
          </p>
        ) : null}

        {update && !update.enabled ? (
          <p className="text-sm text-base-content/60 mt-2">
            Update checks are off. Set UPDATE_CHECK_ENABLED=true to enable
            them.
          </p>
        ) : null}

        {update?.checkFailed ? (
          <p className="text-sm text-base-content/60 mt-2">
            Could not reach GitHub to check for updates.
          </p>
        ) : null}
      </Card.Body>
    </Card>
  );
};

export default AboutTab;
