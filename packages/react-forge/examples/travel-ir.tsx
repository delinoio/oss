import React, { type ReactNode } from "react";
import { fileURLToPath } from "node:url";
import { createSession, Format, type AssetHandle } from "react-forge";
import { Canvas, Cell, Chart, Image, Presentation, Slide, Table, TableRow, Text } from "react-forge/pptx";

// All coordinates and type sizes are in points. The deck is 16:9.
const palette = {
  ink: "#142338", paper: "#F5F1E8", blue: "#2563EB",
  muted: "#586778", white: "#FFFFFF", paleBlue: "#E9EFFB", light: "#CBD5E1",
} as const;

// Business numbers describe a fictional investment case, never actual traction.
const assumptions = {
  travelers: 200_000, tripsPerYear: 1.2, bookingValue: 800, takeRate: 0.08,
  variableCost: 14, acquisitionCost: 25, raise: 1_500_000, runwayMonths: 18,
  quarterlyBookings: [500, 1_500, 3_000, 5_000],
  budget: [
    { name: "Product & engineering", share: 0.5, detail: "Build the itinerary and collaboration core." },
    { name: "Demand experiments", share: 0.25, detail: "Find a repeatable acquisition channel." },
    { name: "Supply & operations", share: 0.15, detail: "Validate booking quality and support." },
    { name: "Contingency", share: 0.1, detail: "Protect room to learn and iterate." },
  ],
} as const;
const unitRevenue = assumptions.bookingValue * assumptions.takeRate;
const contribution = unitRevenue - assumptions.variableCost;
const firstYearBookings = assumptions.quarterlyBookings.reduce((sum, value) => sum + value, 0);
const usd = (value: number) => `$${value.toLocaleString("en-US", { maximumFractionDigits: 0 })}`;

function Copy({ x, y, w, h, size = 20, color = palette.ink, bold = false, children }: {
  x: number; y: number; w: number; h: number; size?: number;
  color?: string; bold?: boolean; children: string;
}) {
  return <Text frame={{ x, y, width: w, height: h }}
    style={{ fontSize: size, color, bold }} overflow="error">{children}</Text>;
}

function Page({ number, title, subtitle, dark = false, note, children }: {
  number: number; title: string; subtitle?: string; dark?: boolean; note?: string; children: ReactNode;
}) {
  const foreground = dark ? palette.white : palette.ink;
  return <Slide background={dark ? palette.ink : palette.paper}><Canvas>
    <Copy x={48} y={32} w={120} h={24} size={15} bold color={dark ? palette.light : palette.muted}>ROAM</Copy>
    <Copy x={48} y={80} w={870} h={106} size={38} bold color={foreground}>{title}</Copy>
    {subtitle && <Copy x={48} y={187} w={850} h={51} size={19} color={dark ? palette.light : palette.muted}>{subtitle}</Copy>}
    {children}
    {note && <Copy x={48} y={489} w={820} h={31} size={11.5} color={dark ? palette.light : palette.muted}>{note}</Copy>}
    <Copy x={880} y={498} w={40} h={20} size={12} color={dark ? palette.light : palette.muted}>{String(number).padStart(2, "0")}</Copy>
  </Canvas></Slide>;
}

function Cover({ coast }: { coast: AssetHandle }) {
  return <Slide background={palette.ink}><Canvas>
    <Image asset={coast} alt="Original concept image of a sunlit Mediterranean coastal village." fit="cover"
      frame={{ x: 444, y: 0, width: 516, height: 540 }} />
    <Copy x={48} y={44} w={320} h={46} size={30} bold color={palette.white}>ROAM</Copy>
    <Copy x={48} y={161} w={380} h={69} size={49} bold color={palette.white}>Less planning.</Copy>
    <Copy x={48} y={225} w={380} h={69} size={49} bold color={palette.white}>More going.</Copy>
    <Copy x={48} y={328} w={340} h={98} size={23} color={palette.light}>A shared trip plan, from inspiration to booking.</Copy>
    <Copy x={48} y={465} w={350} h={49} size={12} color={palette.light}>FICTIONAL COMPANY · ILLUSTRATIVE SEED PITCH · SEPTEMBER 2026</Copy>
  </Canvas></Slide>;
}

function Problem() {
  const items = [
    { title: "Scattered ideas", body: "Saved videos, map pins and booking tabs live in different places.", consequence: "The plan never comes together." },
    { title: "Group friction", body: "Preferences, budgets and decisions disappear into a group chat.", consequence: "One person does all the work." },
    { title: "Booking doubt", body: "A beautiful shortlist still needs a route, timing and availability.", consequence: "Inspiration stalls before action." },
  ];
  return <Page number={2} title="Great trips start with too many tabs."
    subtitle="Customer hypothesis: the gap is turning inspiration into a plan everyone can use."
    note="Problem statements are hypotheses for discovery interviews, not research findings.">
    {items.map((item, i) => <React.Fragment key={item.title}>
      <Copy x={48 + i * 298} y={261} w={270} h={33} size={24} bold>{item.title}</Copy>
      <Copy x={48 + i * 298} y={314} w={259} h={100} size={20} color={palette.muted}>{item.body}</Copy>
      <Copy x={48 + i * 298} y={421} w={260} h={54} size={18} bold color={palette.blue}>{item.consequence}</Copy>
    </React.Fragment>)}
  </Page>;
}

function Product({ product }: { product: AssetHandle }) {
  return <Slide background={palette.paper}><Canvas>
    <Copy x={48} y={32} w={120} h={24} size={15} bold color={palette.muted}>ROAM</Copy>
    <Copy x={48} y={84} w={470} h={59} size={40} bold>One trip.</Copy>
    <Copy x={48} y={144} w={470} h={59} size={40} bold>One shared plan.</Copy>
    <Copy x={48} y={217} w={430} h={101} size={23} color={palette.muted}>A collaborative travel app that turns saved ideas into a bookable itinerary.</Copy>
    <Copy x={48} y={334} w={445} h={33} size={21} bold>Collect places you actually want to go.</Copy>
    <Copy x={48} y={381} w={445} h={33} size={21} bold>Shape the route together.</Copy>
    <Copy x={48} y={428} w={445} h={33} size={21} bold>Book the pieces when you are ready.</Copy>
    <Image asset={product} alt="Generated concept of the proposed ROAM Lisbon weekend itinerary screen, not a shipping application."
      fit="contain" frame={{ x: 570, y: 19, width: 348, height: 502 }} />
    <Copy x={48} y={496} w={760} h={24} size={11.5} color={palette.muted}>Proposed product experience. The interface image is an illustrative concept.</Copy>
    <Copy x={880} y={498} w={40} h={20} size={12} color={palette.muted}>03</Copy>
  </Canvas></Slide>;
}

function Experience() {
  const steps = [
    { title: "Save the spark", body: "Bring a place, a link or a recommendation into one trip." },
    { title: "Make it yours", body: "Set the budget, reorder stops and agree on a daily rhythm." },
    { title: "Go with confidence", body: "Keep plans, booking links and group decisions together." },
  ];
  return <Page number={4} title="A clear path from inspiration to departure." dark
    subtitle="Proposed workflow: lightweight assistance, with travelers in control.">
    {steps.map((step, i) => <React.Fragment key={step.title}>
      <Copy x={48 + i * 298} y={253} w={230} h={58} size={43} color={palette.light}>{`0${i + 1}`}</Copy>
      <Copy x={48 + i * 298} y={323} w={275} h={39} size={24} bold color={palette.white}>{step.title}</Copy>
      <Copy x={48 + i * 298} y={370} w={256} h={92} size={19} color={palette.light}>{step.body}</Copy>
    </React.Fragment>)}
  </Page>;
}

function Market() {
  const opportunity = assumptions.travelers * assumptions.tripsPerYear * unitRevenue;
  return <Page number={5} title="A global habit. A focused first market."
    note="Source: UN Tourism, World Tourism Barometer, January 2026 (20 Jan news release). Arrivals are trips, not unique people.">
    <Copy x={48} y={207} w={350} h={105} size={76} bold color={palette.blue}>1.52B</Copy>
    <Copy x={48} y={320} w={335} h={84} size={23}>International tourist arrivals in 2025, estimated by UN Tourism.</Copy>
    <Copy x={455} y={208} w={440} h={56} size={24} bold>Start with independent city breaks.</Copy>
    <Copy x={455} y={278} w={433} h={60} size={19} color={palette.muted}>English-speaking couples and friends on city breaks. Pilot scope: Lisbon and Barcelona.</Copy>
    <Copy x={455} y={356} w={440} h={56} size={20} bold>{`Illustrative cohort: ${assumptions.travelers.toLocaleString("en-US")} travelers × ${assumptions.tripsPerYear} trips × ${usd(unitRevenue)} revenue`}</Copy>
    <Copy x={455} y={425} w={440} h={45} size={24} bold color={palette.blue}>{`$${(opportunity / 1_000_000).toFixed(2)}M annual revenue potential*`}</Copy>
    <Copy x={48} y={452} w={350} h={27} size={11.5} color={palette.muted}>*Cohort calculation is a scenario, not a market-size estimate.</Copy>
  </Page>;
}

function Economics() {
  return <Page number={6} title="Earn when a useful plan becomes a booking."
    subtitle="Free planning; assumed partner commissions on accommodation and activities."
    note="Illustrative USD model per booked trip. Assumes commission is retained net of cancellations; no partner terms are secured.">
    {[
      { value: usd(assumptions.bookingValue), label: "Booked trip value", detail: "Commissionable GMV" },
      { value: `${assumptions.takeRate * 100}%`, label: "Blended take rate", detail: `${usd(unitRevenue)} net revenue per trip` },
      { value: usd(contribution), label: "Contribution / trip", detail: `After ${usd(assumptions.variableCost)} variable costs` },
    ].map((item, i) => <React.Fragment key={item.label}>
      <Copy x={48 + i * 298} y={266} w={275} h={87} size={58} bold color={palette.blue}>{item.value}</Copy>
      <Copy x={48 + i * 298} y={365} w={276} h={37} size={22} bold>{item.label}</Copy>
      <Copy x={48 + i * 298} y={409} w={270} h={57} size={18} color={palette.muted}>{item.detail}</Copy>
    </React.Fragment>)}
  </Page>;
}

function Distribution() {
  const rows = [
    ["Travel creators", "Publish useful, remixable city itineraries.", "Cost per first booked trip"],
    ["Trip invitations", "Let every organizer bring their group.", "Invite-to-active conversion"],
    ["High-intent search", "Answer specific short-break planning needs.", "Plan-to-book conversion"],
  ];
  return <Page number={7} title="Win the next trip through a useful plan."
    subtitle="Three measurable acquisition experiments, before broad paid expansion."
    note="Proposed experiments. Scale a channel only after retained contribution exceeds acquisition cost.">
    <Table frame={{ x: 48, y: 259, width: 864, height: 208 }} columns={[184, 390, 290]}
      style={{ fontSize: 18, color: palette.ink }}>
      <TableRow>{["Channel", "First experiment", "Decision metric"].map(label =>
        <Cell key={label} fill={palette.ink} style={{ bold: true, color: palette.white }}>{label}</Cell>)}</TableRow>
      {rows.map((row, i) => <TableRow key={row[0]}>{row.map((value, j) =>
        <Cell key={j} fill={i % 2 === 0 ? palette.white : palette.paleBlue} style={{ bold: j === 0 }}>{value}</Cell>)}</TableRow>)}
    </Table>
  </Page>;
}

function Milestones() {
  return <Page number={8} title="Prove repeat use before scaling."
    subtitle={`First-year operating targets: ${firstYearBookings.toLocaleString("en-US")} booked trips and ${usd(firstYearBookings * unitRevenue)} net revenue.`}
    note="All figures are targets, not traction or a forecast. Repeat rate means a second booking within 90 days of the first.">
    <Chart frame={{ x: 48, y: 250, width: 535, height: 228 }} orientation="vertical" legend="hidden" dataLabels="value"
      data={{ categories: ["Q1", "Q2", "Q3", "Q4"], series: [{ key: "bookings", name: "Target booked trips per quarter", values: [...assumptions.quarterlyBookings] }] }} />
    <Copy x={640} y={250} w={271} h={37} size={28} bold color={palette.blue}>{`${usd(assumptions.acquisitionCost)} CAC`}</Copy>
    <Copy x={640} y={293} w={258} h={55} size={18} color={palette.muted}>Target blended acquisition cost per first-time booker.</Copy>
    <Copy x={640} y={365} w={271} h={37} size={28} bold color={palette.blue}>25% repeat</Copy>
    <Copy x={640} y={409} w={258} h={57} size={18} color={palette.muted}>Q4 target among customers with a full 90-day window.</Copy>
  </Page>;
}

function Advantage() {
  return <Page number={9} title="The shared trip is the product advantage." dark
    subtitle="The proposed wedge: own the group's plan before the booking decision.">
    <Copy x={48} y={265} w={397} h={169} size={31} bold color={palette.white}>A plan that reflects the people going, not just the places available.</Copy>
    {[
      ["Group context", "Budget, pace and places in one workspace."],
      ["Useful decisions", "Every edit makes the itinerary more specific."],
      ["Repeat value", "Reusable preferences make the next trip easier."],
    ].map(([title, body], i) => <React.Fragment key={title}>
      <Copy x={504} y={254 + i * 80} w={386} h={32} size={22} bold color={palette.white}>{title!}</Copy>
      <Copy x={504} y={287 + i * 80} w={386} h={47} size={17} color={palette.light}>{body!}</Copy>
    </React.Fragment>)}
    <Copy x={48} y={435} w={377} h={41} size={16} color={palette.light}>A differentiation hypothesis to validate through use.</Copy>
  </Page>;
}

function Roadmap() {
  const stages = [
    { period: "Months 1–3", title: "Validate the need", action: "Interview travelers and test a concierge planning pilot.", gate: "Gate: shared plans get used." },
    { period: "Months 4–9", title: "Prove the loop", action: "Launch collaborative itineraries and booking attribution.", gate: "Gate: repeat use and booking intent." },
    { period: "Months 10–18", title: "Scale with discipline", action: "Expand only the channels and destinations that work.", gate: "Gate: contribution covers CAC." },
  ];
  return <Page number={10} title="An 18-month plan to retire the key risks."
    subtitle="Sequence learning before expansion; milestones release the next stage of spend."
    note="Principal risks: weak repeat demand, commission economics and expensive acquisition. Gates are proposed, not achieved.">
    {stages.map((stage, i) => <React.Fragment key={stage.period}>
      <Copy x={48 + i * 298} y={260} w={276} h={32} size={20} bold color={palette.blue}>{stage.period}</Copy>
      <Copy x={48 + i * 298} y={307} w={275} h={70} size={25} bold>{stage.title}</Copy>
      <Copy x={48 + i * 298} y={378} w={263} h={75} size={18} color={palette.muted}>{stage.action}</Copy>
      <Copy x={48 + i * 298} y={453} w={276} h={30} size={14} bold>{stage.gate}</Copy>
    </React.Fragment>)}
  </Page>;
}

function Ask() {
  return <Page number={11} title="Raise $1.5M to prove the business."
    note="Illustrative fundraise and use of funds. Budget totals $1.5M; 18-month runway assumes average cash use of approximately $83K/month.">
    <Copy x={48} y={224} w={330} h={97} size={69} bold color={palette.blue}>{`$${assumptions.raise / 1_000_000}M`}</Copy>
    <Copy x={48} y={330} w={330} h={45} size={29} bold>{`${assumptions.runwayMonths} months of focus`}</Copy>
    <Copy x={48} y={391} w={330} h={85} size={20} color={palette.muted}>Proposed core team: product, engineering and travel operations. No team credentials are implied.</Copy>
    {assumptions.budget.map((item, i) => <React.Fragment key={item.name}>
      <Copy x={450} y={220 + i * 65} w={345} h={31} size={20} bold>{`${item.share * 100}%  ${item.name}`}</Copy>
      <Copy x={807} y={221 + i * 65} w={106} h={31} size={20} bold color={palette.blue}>{`$${assumptions.raise * item.share / 1_000}K`}</Copy>
      <Copy x={450} y={253 + i * 65} w={459} h={28} size={16} color={palette.muted}>{item.detail}</Copy>
    </React.Fragment>)}
  </Page>;
}

function Close({ horizon }: { horizon: AssetHandle }) {
  return <Slide background={palette.ink}><Canvas>
    <Image asset={horizon} alt="Original concept image of an Alpine lake seen through a train window." fit="cover"
      frame={{ x: 450, y: 0, width: 510, height: 540 }} />
    <Copy x={48} y={44} w={330} h={45} size={30} bold color={palette.white}>ROAM</Copy>
    <Copy x={48} y={160} w={354} h={180} size={45} bold color={palette.white}>Make room for the trip itself.</Copy>
    <Copy x={48} y={366} w={345} h={75} size={22} color={palette.light}>Start with a shared plan. Build a reason to come back.</Copy>
    <Copy x={48} y={480} w={347} h={32} size={12} color={palette.light}>FICTIONAL INVESTMENT CASE · CREATED WITH REACT FORGE</Copy>
  </Canvas></Slide>;
}

export default async function task({ signal }: { signal: AbortSignal }) {
  signal.throwIfAborted();
  const session = createSession(Format.Pptx);
  try {
    // Resolve against this module so the task works from any caller directory.
    const [coast, product, horizon] = await Promise.all(
      ["coast.png", "product.png", "horizon.png"].map(name => session.registerImage({
        path: fileURLToPath(new URL(`./travel-ir-assets/${name}`, import.meta.url)),
      }, { signal })),
    );
    signal.throwIfAborted();
    await session.render(<Presentation width={960} height={540} fontFamily="Avenir Next">
      <Cover coast={coast!} /><Problem /><Product product={product!} /><Experience />
      <Market /><Economics /><Distribution /><Milestones /><Advantage /><Roadmap /><Ask />
      <Close horizon={horizon!} />
    </Presentation>);
    return session;
  } catch (error) {
    // A task that fails before returning its session still owns disposal.
    await session.dispose();
    throw error;
  }
}
