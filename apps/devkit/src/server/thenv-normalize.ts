const allowedRoles = new Set(["reader", "writer", "admin"]);

export function normalizeRoleValue(role: string): string {
  const normalized = role.trim().toLowerCase();
  if (!allowedRoles.has(normalized)) {
    return "reader";
  }
  return normalized;
}

export function parseAuditEventLabel(value: string): string {
  const normalized = value.trim().toLowerCase();
  switch (normalized) {
    case "1":
    case "push":
      return "push";
    case "2":
    case "pull":
      return "pull";
    case "3":
    case "list":
      return "list";
    case "4":
    case "rotate":
      return "rotate";
    case "5":
    case "activate":
      return "activate";
    case "6":
    case "policy-update":
      return "policy-update";
    default:
      return "unspecified";
  }
}
