const allowedRoles = new Set(["reader", "writer", "admin"]);

export function normalizeRoleValue(role) {
  const normalized = String(role).trim().toLowerCase();
  if (!allowedRoles.has(normalized)) {
    return "reader";
  }
  return normalized;
}

export function roleCodeFromValue(role) {
  switch (normalizeRoleValue(role)) {
    case "writer":
      return 2;
    case "admin":
      return 3;
    default:
      return 1;
  }
}

export function roleLabelFromUnknown(value) {
  if (typeof value === "number") {
    switch (value) {
      case 2:
        return "writer";
      case 3:
        return "admin";
      default:
        return "reader";
    }
  }
  return normalizeRoleValue(String(value));
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

export function bundleStatusLabel(value) {
  if (typeof value === "number") {
    switch (value) {
      case 1:
        return "active";
      case 2:
        return "archived";
      default:
        return "unspecified";
    }
  }

  const normalized = String(value).trim().toLowerCase();
  if (normalized === "1") {
    return "active";
  }
  if (normalized === "2") {
    return "archived";
  }
  if (normalized === "active" || normalized === "archived") {
    return normalized;
  }
  return "unspecified";
}
