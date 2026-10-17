export function attachProbeCancellation(controller, owner = process) {
  const interrupt = () => controller.abort();
  const message = (value) => {
    if (value?.type === "abort") interrupt();
  };
  owner.on("SIGINT", interrupt);
  owner.on("SIGTERM", interrupt);
  owner.on("disconnect", interrupt);
  owner.on("message", message);
  return () => {
    owner.removeListener("SIGINT", interrupt);
    owner.removeListener("SIGTERM", interrupt);
    owner.removeListener("disconnect", interrupt);
    owner.removeListener("message", message);
  };
}
