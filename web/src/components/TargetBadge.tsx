import { parseTargetId } from "../utils";
import { Badge } from "./ui/badge";

interface TargetBadgeProps {
  targetId: string;
}

export function TargetBadge({ targetId }: TargetBadgeProps) {
  const parsed = parseTargetId(targetId);

  if (!parsed) {
    // Fallback for unknown format
    return (
      <Badge variant="secondary">
        {targetId}
      </Badge>
    );
  }

  const typeVariant = {
    volume: "default" as const,
    db: "secondary" as const,
  };

  const typeLabel = {
    volume: "volume",
    db: "db",
  };

  return (
    <span
      className="inline-flex cursor-default items-center rounded-full overflow-hidden border border-gray-300"
      title={targetId}
    >
      <Badge variant={typeVariant[parsed.type]} className="rounded-none border-0">
        {typeLabel[parsed.type]}
      </Badge>
      <span className="bg-gray-100 px-2 py-0.5 text-xs text-gray-800">
        {parsed.name}
      </span>
    </span>
  );
}
