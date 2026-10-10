// SPDX-License-Identifier: Apache-2.0
/** Presentation classifications only; labels never authorize an operation. */
export enum ProductReferenceKind {
  Agent = "agent", Template = "template", Snapshot = "snapshot", Schedule = "schedule", Resource = "resource", Session = "session", Project = "project", Repository = "repository",
  Account = "account", Provider = "provider", Worker = "worker", Server = "server",
  Device = "device", Execution = "execution", Input = "input", Operation = "operation",
  Backup = "backup", Connection = "connection", Model = "model",
}
const english: Record<ProductReferenceKind, string> = {
  agent: "Agent", template: "Template", snapshot: "Snapshot", schedule: "Schedule", resource: "Resource", session: "Session", project: "Project", repository: "Repository",
  account: "Account", provider: "Provider", worker: "Worker", server: "Server", device: "Device",
  execution: "Execution", input: "Input", operation: "Operation", backup: "Backup", connection: "Connection", model: "Model",
};
const korean: Record<ProductReferenceKind, string> = {
  agent: "에이전트", template: "템플릿", snapshot: "스냅샷", schedule: "일정", resource: "리소스", session: "세션", project: "프로젝트", repository: "저장소", account: "계정",
  provider: "제공자", worker: "워커", server: "서버", device: "기기", execution: "실행",
  input: "입력", operation: "작업", backup: "백업", connection: "연결", model: "모델",
};
/** Own one instance per connection/view. Never reuse numbers for another target. */
export class ProductReferenceLabels {
  private readonly numbers = new Map<string, number>();
  label(id: string | undefined, kind = ProductReferenceKind.Resource, language = "en", name?: string): string {
    // Names are original user content, including UUID-looking names.
    if (name) return name;
    const noun = (language.startsWith("ko") ? korean : english)[kind];
    if (!id) return language.startsWith("ko") ? `${noun} 정보 없음` : `${noun} unavailable`;
    const key = `${kind}:${id}`;
    let number = this.numbers.get(key);
    if (number === undefined) { number = this.numbers.size + 1; this.numbers.set(key, number); }
    return `${noun} ${number}`;
  }
}
/** Use ONLY on DeliDev-generated diagnostic prose, never user/native content. */
export function productDiagnosticText(value: string): string {
  return value.replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi, "[…]");
}

export function unnamedProductReference(kind = ProductReferenceKind.Resource, language = "en"): string {
  const noun = (language.startsWith("ko") ? korean : english)[kind];
  return language.startsWith("ko") ? `이름 없는 ${noun}` : `Unnamed ${noun.toLowerCase()}`;
}
