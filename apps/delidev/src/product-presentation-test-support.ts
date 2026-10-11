// SPDX-License-Identifier: Apache-2.0
/** Match safe labels in fixtures while mutation assertions still bind exact IDs. */
export function safePresentation(text: string): RegExp {
  const uuid = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;
  const pieces = text
    .split(uuid)
    .map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  const result = pieces.join(
    "(?:Record|Project|Repository|Session|Account|Provider|Runner Device|Terminal|Backup|Input|Agent Worker|Model|Device|Server|프로젝트|기록|계정|저장소|세션|백업|서버|디바이스) \\d+",
  );
  return new RegExp(result.replace(/\\\./g, "(?: · \\d+)?\\."));
}
