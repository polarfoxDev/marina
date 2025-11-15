interface TargetBadgeProps {
  targetId: string;
}

interface ParsedTarget {
  type: "vol" | "dbs";
  name: string;
  id?: string;
}

export function parseTargetId(targetId: string): ParsedTarget | null {
  // Format: vol:name or dbs:name:id
  const parts = targetId.split(":");

  if (parts[0] === "vol" && parts.length === 2) {
    return {
      type: "vol",
      name: parts[1],
    };
  }

  if (parts[0] === "dbs" && parts.length >= 3) {
    return {
      type: "dbs",
      name: parts[1],
      id: parts.slice(2).join(":"), // Rejoin in case ID contains colons
    };
  }

  return null;
}

export function formatTargetName(targetId: string): string {
  const parsed = parseTargetId(targetId);
  return parsed ? parsed.name : targetId;
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
    vol: "bg-blue-100 text-blue-700",
    dbs: "bg-purple-100 text-purple-700",
  };

  const typeLabel = {
    vol: "vol",
    dbs: "db",
  };

  return (
    <span
      className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-gray-100 text-gray-700"
      title={
        parsed.id ? `${parsed.type}:${parsed.name}:${parsed.id}` : targetId
      }
    >
      <span
        className={`px-1 py-0.5 rounded text-xs font-semibold ${
          typeColors[parsed.type]
        }`}
      >
        {typeLabel[parsed.type]}
      </span>
      <span>{parsed.name}</span>
    </span>
  );
}
