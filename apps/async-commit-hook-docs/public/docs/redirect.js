const sections = new Set(["install", "start", "configuration", "validation", "commands", "agents", "web", "privacy", "recovery", "compatibility", "symlinks", "existing-hooks"]);
const section = window.location.hash.slice(1);
window.location.replace(sections.has(section) ? "/" + section : "/");
