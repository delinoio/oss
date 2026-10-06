import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { test, expect, afterEach, vi } from "vitest";
import type { Resource } from "@delinoio/delidev-api-client";
import { NativeGrokInteraction, NativeGrokTool, NativeGrokToolsTerminal } from "./native-grok-interactions";
import { encode, object, type Document } from "./documents";
const send = vi.fn();vi.mock("./mutation", () => ({useRetainedMutation: () => ({send,busy:false,uncertain:false,error:undefined})}));
afterEach(()=>{cleanup();send.mockClear();});
const id="01900000-0000-7000-8000-000000000001";
function fixture():Document{return {execution_id:id,native_thread_id:id,native_turn_id:"526452fa-1956-42dd-b5f4-60e2b23dfe92",native_item_id:"original",native_request_id:{kind:"text",text:"native-original"},type:"user-question",closure:"open",grok:{version:"1.0.41",observation_id:id,request_digest:"ab".repeat(32),proposal_digest:"cd".repeat(32),event:{method:"_x.ai/ask_user_question",request_id:{kind:"text",text:"native-original"},arrival_id:id,payload:{sessionId:id,toolCallId:"original",questions:[{question:"Original <script>question()</script>?",options:[{label:"One",description:"First"},{label:"Two",description:"Second"}],multiSelect:null}]}}}};}

function publishedFixture(kind: "question" | "write" | "plan"): Document {
 const data = fixture(), retained = object(data.grok), event = object(retained.event), payload = object(event.payload);
 data.questions = null;
 if (kind !== "question") {
  data.type = "native-approval";
  delete payload.questions;
  if (kind === "plan") {
   event.method = "_x.ai/exit_plan_mode";
   payload.planContent = "# Original <script>plan()</script>";
   retained.plan = {origin: {entry_tool_id: "entry", entry_event_id: `${id}-1`, revision: 1}, write_tool_id: "write", content_digest: "ab".repeat(32)};
  } else {
   event.method = "session/request_permission";
   delete payload.toolCallId;
   payload.toolCall = {toolCallId: "original"};
   payload.options = [
    {optionId: "allow-edits-session", name: "Yes, allow all edits during this session", kind: "allow_always"},
    {optionId: "allow-once", name: "Yes", kind: "allow_once"},
    {optionId: "reject-once", name: "No, and tell Grok what to do differently", kind: "reject_once"},
   ];
  }
 }
 // Retain noncanonical native bytes rather than reconstructing proposal authority.
 event.proposal_json = ` \n${JSON.stringify(payload, null, 2)}\n `;
 return data;
}

for (const kind of ["question", "write", "plan"] as const) test(`renders published ${kind} request and transcript with inert original proposal bytes`, () => {
 const data = publishedFixture(kind), event = object(object(data.grok).event);
 const resource = {id, revision: 1n, schemaVersion: 1, documentJson: encode(data)} as Resource;
 const {container} = render(<><NativeGrokInteraction data={data} resource={resource} accepted={() => {}} /><NativeGrokTool data={{role: "tool", state: "complete", execution_id: id, native_thread_id: id, native_turn_id: data.native_turn_id, grok_tool: event}} /></>);
 expect(screen.getByLabelText("Original Grok request")).toBeTruthy();
 expect(screen.getByLabelText("Original Grok tool observation")).toBeTruthy();
 expect(screen.getByLabelText(kind === "question" ? "Exact answer for question 1" : "Decision")).toBeTruthy();
 expect(container.querySelector("script")).toBeNull();
 expect(event.proposal_json).toBe(` \n${JSON.stringify(event.payload, null, 2)}\n `);
});

for (const value of [null, {}, "", "bad\0bytes", "\uD800", "질".repeat(175000)]) test(`rejects invalid proposal bytes ${typeof value}:${String(value).slice(0, 20)}`, () => {
 const data = publishedFixture("question"), event = object(object(data.grok).event);
 event.proposal_json = value;
 render(<NativeGrokInteraction data={data} />);
 expect(screen.getByLabelText("Grok request unavailable")).toBeTruthy();
});

test("still rejects unrelated keys alongside the original proposal", () => {
 const data = publishedFixture("question");
 object(object(data.grok).event).foreign_authority = true;
 render(<NativeGrokInteraction data={data} />);
 expect(screen.getByLabelText("Grok request unavailable")).toBeTruthy();
});

test("does not accept proposal bytes on native notifications", () => {
 render(<NativeGrokTool data={{role: "tool", state: "complete", execution_id: id, native_thread_id: id, native_turn_id: fixture().native_turn_id, grok_tool: {method: "session/update", payload: {sessionId: id, update: {sessionUpdate: "current_mode_update"}}, proposal_json: "{}"}}} />);
 expect(screen.getByLabelText("Grok tool unavailable")).toBeTruthy();
});
test("renders nullable native questions and continues cancellation distinctly",()=>{const data=fixture();const resource={id,revision:9007199254740993n,schemaVersion:1,documentJson:encode(data)} as Resource;const {container}=render(<NativeGrokInteraction data={data} resource={resource} accepted={()=>{}}/>);expect(screen.getByText("Native multi-select value is null")).toBeTruthy();expect(container.querySelector("script")).toBeNull();fireEvent.change(screen.getByLabelText("Question response"),{target:{value:"cancelled"}});fireEvent.click(screen.getByText("Send response to Grok"));const input=send.mock.calls[0]![0];expect(input.mutation.expectedRevision).toBe(9007199254740993n);expect(JSON.parse(new TextDecoder().decode(input.responseJson))).toEqual({grok:{outcome:"cancelled"}});});
test("sends exact original answer keys and notes",()=>{const data=fixture(),resource={id,revision:1n,schemaVersion:1,documentJson:encode(data)} as Resource;render(<NativeGrokInteraction data={data} resource={resource} accepted={()=>{}}/>);fireEvent.change(screen.getByLabelText("Exact answer for question 1"),{target:{value:"One, Two"}});fireEvent.change(screen.getByLabelText("Notes for question 1"),{target:{value:"Original notes"}});fireEvent.click(screen.getByText("Send response to Grok"));expect(JSON.parse(new TextDecoder().decode(send.mock.calls[0]![0].responseJson))).toEqual({grok:{outcome:"accepted",answers:{"Original <script>question()</script>?":"One, Two"},annotations:{"Original <script>question()</script>?":{notes:"Original notes"}}}});});
for(const change of ["mixed","request","kind","version","arrival","null"])test(`rejects ${change} before exposing controls`,()=>{const data=fixture(),r=object(data.grok),v=object(r.event);if(change==="mixed")data.claude={};if(change==="request")data.native_request_id={kind:"text",text:"foreign"};if(change==="kind")data.type="native-approval";if(change==="version")r.version="future";if(change==="arrival")v.arrival_id="foreign";if(change==="null")data.grok=null;render(<NativeGrokInteraction data={data}/>);expect(screen.getByLabelText("Grok request unavailable")).toBeTruthy();});
test("shows native revised Plan without inventing another approval",()=>{const data=fixture(),r=object(data.grok),v=object(r.event),p=object(v.payload);data.type="native-approval";v.method="_x.ai/exit_plan_mode";delete p.questions;p.planContent="# Revised native plan";r.plan={origin:{entry_tool_id:"entry",entry_event_id:`${id}-1`,revision:2},write_tool_id:"write-revision",content_digest:"ab".repeat(32)};const {container}=render(<NativeGrokInteraction data={data}/>);expect(screen.getByText("Plan revision 2")).toBeTruthy();expect(screen.getByText("# Revised native plan")).toBeTruthy();expect(container.querySelector("button,a")).toBeNull();});

test("keeps exact native tools accounting and cleanup independent", () => {
 const counts={input_tokens:"18446744073709551615",output_tokens:"1",cache_read_input_tokens:"0",cache_creation_input_tokens:"0",reasoning_tokens:"0"};
 const progress:Document={execution_id:id,native_thread_id:id,native_turn_id:"526452fa-1956-42dd-b5f4-60e2b23dfe92",outcome:"succeeded",observed:{model:"original-model"},grok_content:{responses:1},grok_response_totals:counts,grok_tools_terminal:{kind:"closed-first-tools",native_event_id:`${id}-10`,timestamp_ms:"1",elapsed_ms:"2",model:"original-model",reason:"end_turn",counts,total_tokens:"16",model_calls:"1",api_duration_ms:"2",turns:"1"}};
 const {rerender}=render(<NativeGrokToolsTerminal progress={progress}/>);
 expect(screen.getByText("18446744073709551615")).toBeTruthy();
 expect(screen.getByText(/Workspace cleanup is unconfirmed/)).toBeTruthy();
 const foreign={...progress,grok_response_totals:{...counts,input_tokens:"1"}};
 rerender(<NativeGrokToolsTerminal progress={foreign}/>);
 expect(screen.getByText(/terminal is unavailable or inconsistent/)).toBeTruthy();
});

test("renders the server's null generic question field", () => {
 const data = fixture();
 data.questions = null;
 const resource = {id, revision: 1n, schemaVersion: 1, documentJson: encode(data)} as Resource;
 render(<NativeGrokInteraction data={data} resource={resource} accepted={() => {}} />);
 expect(screen.getByLabelText("Exact answer for question 1")).toBeTruthy();
});

test("renders every bounded native question and option", () => {
 const data = fixture(), payload = object(object(object(data.grok).event).payload);
 payload.questions = Array.from({length: 32}, (_, i) => ({
  question: `Question ${i + 1}`,
  options: Array.from({length: i === 0 ? 64 : 1}, (_, j) => ({label: `Option ${j + 1}`, description: ""})),
  multiSelect: i === 0 ? true : null,
 }));
 const resource = {id, revision: 1n, schemaVersion: 1, documentJson: encode(data)} as Resource;
 render(<NativeGrokInteraction data={data} resource={resource} accepted={() => {}} />);
 expect(screen.getByLabelText("Exact answer for question 32")).toBeTruthy();
 expect(screen.getByText("Option 64 —")).toBeTruthy();
});

test("retains native UTF-8 question, label and description bounds", () => {
 const data = fixture(), payload = object(object(object(data.grok).event).payload);
 const question = "질".repeat(5461) + "a";
 const label = "선".repeat(1365) + "a";
 payload.questions = [{question, options: [{label, description: "설".repeat(5461) + "a"}], multiSelect: false}];
 const resource = {id, revision: 1n, schemaVersion: 1, documentJson: encode(data)} as Resource;
 render(<NativeGrokInteraction data={data} resource={resource} accepted={() => {}} />);
 expect(screen.getByLabelText("Exact answer for question 1")).toBeTruthy();
});

for (const spelling of ["-0", "9223372036854775808", "-9223372036854775809"]) test(`retains numeric request ${spelling} without rounding`, () => {
 const data = fixture(), event = object(object(data.grok).event);
 data.native_request_id = event.request_id = {kind: "decimal", decimal: spelling};
 render(<NativeGrokInteraction data={data} />);
 expect(screen.getByLabelText("Original Grok request")).toBeTruthy();
 expect(screen.getByText(new RegExp(spelling))).toBeTruthy();
});

test("focuses the native Plan approval decision on display", () => {
 const data = fixture(), retained = object(data.grok), event = object(retained.event), payload = object(event.payload);
 data.questions = null;
 data.type = "native-approval";
 event.method = "_x.ai/exit_plan_mode";
 delete payload.questions;
 payload.planContent = "# Original plan";
 retained.plan = {origin: {entry_tool_id: "entry", entry_event_id: `${id}-1`, revision: 1}, write_tool_id: "write", content_digest: "ab".repeat(32)};
 const resource = {id, revision: 1n, schemaVersion: 1, documentJson: encode(data)} as Resource;
 render(<NativeGrokInteraction data={data} resource={resource} accepted={() => {}} />);
 expect(document.activeElement).toBe(screen.getByLabelText("Decision"));
});

test("resets Grok overflow guidance when the selected interaction changes", () => {
 const data = fixture();
 const resource = { id, revision: 1n, schemaVersion: 1, documentJson: encode(data) } as Resource;
 const nextResource = { ...resource, id: "01900000-0000-7000-8000-000000000002" };
 const view = () => <NativeGrokInteraction data={data} resource={resource} accepted={() => {}} />;
 const { rerender } = render(view());
 fireEvent.change(screen.getByLabelText("Exact answer for question 1"), { target: { value: "x".repeat((64 << 10) + 1) } });
 expect(screen.getByText(/Your previous draft was kept/)).toBeTruthy();
 rerender(<NativeGrokInteraction data={data} resource={nextResource} accepted={() => {}} />);
 expect(screen.queryByText(/Your previous draft was kept/)).toBeNull();
});
