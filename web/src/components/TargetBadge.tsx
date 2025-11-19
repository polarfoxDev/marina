import { parseTargetId } from "../utils";

interface TargetBadgeProps {
  targetId: string;
}

export function TargetBadge({ targetId }: TargetBadgeProps) {
  const parsed = parseTargetId(targetId);

  if (!parsed) {
    // Fallback for unknown format
    return (
      <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-700">
        {targetId}
      </span>
    );
  }

  const typeColors = {
    volume: "bg-blue-100 text-blue-700",
    db: "bg-purple-100 text-purple-700",
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
      <span
        className={`px-2 py-0.5 text-xs font-medium ${typeColors[parsed.type]}`}
      >
        {typeLabel[parsed.type]}
      </span>
      <span className="bg-gray-100 px-2 py-0.5 text-xs text-gray-800">
        {parsed.name}
      </span>
    </span>
  );
}
