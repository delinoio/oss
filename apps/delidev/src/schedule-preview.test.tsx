// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ScheduleCalendarSchema, ScheduleDayMatch, ScheduleService } from "@delinoio/delidev-api-client";
import { SchedulePreview, calendarSentence, validCalendar } from "./schedule-preview";
import { i18n } from "./localization";

const range=(first:number,last:number)=>Array.from({length:last-first+1},(_,i)=>first+i);
const calendar=()=>create(ScheduleCalendarSchema,{ minutes:[0],hours:[9],daysOfMonth:range(1,31),months:range(1,12),weekdays:range(1,5),dayMatch:ScheduleDayMatch.AND });
const result=()=>({calendar:calendar(),calculatedAt:timestampFromDate(new Date("2026-10-10T09:00:00Z")),nextRunAt:timestampFromDate(new Date("2026-10-12T09:00:00Z"))});
afterEach(async()=>{await i18n.changeLanguage("en");});
function fixture(handler:()=>ReturnType<typeof result>|Promise<ReturnType<typeof result>>) {
 const preview=vi.fn(handler), save=vi.fn();
 const transport=createRouterTransport(router=>router.service(ScheduleService,{previewScheduleCalendar:preview,saveSchedule:save}));
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view=(props:Partial<React.ComponentProps<typeof SchedulePreview>>={},selected:Transport=transport)=><TransportProvider transport={selected}><QueryClientProvider client={client}><SchedulePreview cron="0 9 * * 1-5" timezone="UTC" enabled fallback="Preset fallback" {...props}/></QueryClientProvider></TransportProvider>;
 return {preview,save,transport,view};
}
it("shows server-derived recurrence, provisional date and activation without saving",async()=>{
 const f=fixture(result), rendered=render(f.view());
 expect(screen.getByRole("status").textContent).toContain("Calculating");
 expect(f.preview).not.toHaveBeenCalled();
 expect(await screen.findByText("Every Monday to Friday at 9:00 AM")).toBeTruthy();
 expect(screen.getByRole("status").textContent).toContain("October 12, 2026");
 expect(screen.getByRole("status").textContent).toMatch(/in 2 days/i);
 expect(screen.getByText("After you save this schedule")).toBeTruthy();
 const disclosure=screen.getByRole("button",{name:"Cron details"});expect(disclosure.getAttribute("aria-expanded")).toBe("false");
 fireEvent.click(disclosure);expect(disclosure.getAttribute("aria-expanded")).toBe("true");
 rendered.rerender(f.view({enabled:false}));expect(screen.getByText("After you enable this schedule")).toBeTruthy();expect(screen.getByRole("status").textContent).toBe("No automatic run is scheduled.");
 expect(f.save).not.toHaveBeenCalled();
});
it("clears dates immediately for edited, invalid and disconnected drafts",async()=>{
 const f=fixture(result),rendered=render(f.view());await screen.findByText(/Every Monday to Friday/);
 rendered.rerender(f.view({cron:"0 10 * * *"}));expect(screen.getByRole("status").textContent).not.toContain("October");
 rendered.rerender(f.view({invalid:true}));expect(screen.getByRole("status").textContent).toContain("valid five-field");
 rendered.rerender(f.view({connectionReady:false}));expect(screen.getByRole("status").textContent).toContain("unavailable");
 expect(f.save).not.toHaveBeenCalled();
});
it("fences late responses across exact inputs, replaced transport and disposal",async()=>{
 let settle!:(value:ReturnType<typeof result>)=>void;
 const f=fixture(()=>new Promise(resolve=>{settle=resolve;}));const rendered=render(f.view());await waitFor(()=>expect(f.preview).toHaveBeenCalledTimes(1));
 const newer=createRouterTransport(router=>router.service(ScheduleService,{previewScheduleCalendar:()=>{throw new ConnectError("older server",Code.Unimplemented);}}));
 rendered.rerender(f.view({cron:"0 11 * * *"},newer));
 await act(async()=>{settle(result());});expect(screen.getByRole("status").textContent).not.toContain("October");
 expect(await screen.findByText("Preview unavailable; saving determines the next run.")).toBeTruthy();
 expect(screen.getByText("Preset fallback")).toBeTruthy();rendered.unmount();expect(f.save).not.toHaveBeenCalled();
});
it.each([Code.InvalidArgument,Code.Unavailable,Code.Unimplemented])("does not publish a date after failure %s",async(code)=>{
 const f=fixture(()=>{throw new ConnectError("fixture",code);});render(f.view());await waitFor(()=>expect(f.preview).toHaveBeenCalledTimes(1));
 await waitFor(()=>expect(screen.getByRole("status").textContent).not.toContain("Calculating"));expect(screen.getByRole("status").textContent).not.toContain("October");
});
it("refreshes when the server-relative occurrence boundary is reached",async()=>{
 let count=0;
 const f=fixture(()=>{const response=result();if(count++===0)response.nextRunAt=timestampFromDate(new Date("2026-10-10T09:00:01Z"));return response;});render(f.view());
 await waitFor(()=>expect(f.preview).toHaveBeenCalledTimes(2),{timeout:2500});expect(await screen.findByText(/October 12, 2026/)).toBeTruthy();
});
it("preserves all complex constraints and parser AND/OR in English and Korean",async()=>{
 const c=create(ScheduleCalendarSchema,{minutes:[5,20,25,30],hours:[9,10,11],daysOfMonth:[1,15],months:[1,3],weekdays:[1],dayMatch:ScheduleDayMatch.OR});
 expect(validCalendar(c)).toBe(true);let prose=calendarSentence(c);expect(prose).toContain("Either");expect(prose).toContain("1, 15");expect(prose).toContain("Monday");expect(prose).toContain("January and March");expect(prose).toContain("5, 20, 25, 30");
 c.dayMatch=ScheduleDayMatch.AND;expect(calendarSentence(c)).toContain("Both");
 await i18n.changeLanguage("ko");c.dayMatch=ScheduleDayMatch.OR;prose=calendarSentence(c);expect(prose).toContain("또는");expect(prose).toContain("월요일");
 c.minutes=[20,5];expect(validCalendar(c)).toBe(false);
});
it("rejects malformed or nonfuture server observations",async()=>{
 const f=fixture(()=>{const response=result();response.nextRunAt=response.calculatedAt;return response;});render(f.view());
 expect(await screen.findByText("Preview unavailable. Saving determines the next run.")).toBeTruthy();expect(screen.getByRole("status").textContent).not.toContain("October");
});
