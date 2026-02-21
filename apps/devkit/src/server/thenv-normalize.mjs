const allowedRoles = new Set(["reader", "writer", "admin"]);

export function normalizeRoleValue(role) {
  const normalized = String(role).trim().toLowerCase();
  if (!allowedRoles.has(normalized)) {
    return "reader";
  }
  return normalized;
}

export function parseAuditEventLabel(value) {
  const normalized = String(value).trim().toLowerCase();
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
