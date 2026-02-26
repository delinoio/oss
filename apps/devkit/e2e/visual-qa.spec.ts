import { test as base } from "@playwright/test";
import { PlaywrightAiFixture } from "@midscene/web/playwright";
import type { PlayWrightAiFixtureType } from "@midscene/web/playwright";

type VisualScenario = {
  readonly id: string;
  readonly path: string;
  readonly assertions: readonly string[];
};

const REQUIRED_MIDSCENE_ENV = [
  "MIDSCENE_MODEL_BASE_URL",
  "MIDSCENE_MODEL_API_KEY",
  "MIDSCENE_MODEL_NAME",
  "MIDSCENE_MODEL_FAMILY",
] as const;

const test = base.extend<PlayWrightAiFixtureType>(PlaywrightAiFixture());

const VISUAL_SCENARIOS: readonly VisualScenario[] = [
  {
    id: "devkit-home",
    path: "/",
    assertions: [
      "The page has a clear top-level title and primary call-to-action in the first viewport.",
      "No text is clipped, cut off, or overlapping with neighboring UI elements.",
      "Interactive controls are visually distinguishable and readable.",
    ],
  },
  {
    id: "commit-tracker",
    path: "/apps/commit-tracker",
    assertions: [
      "Table headers and rows are aligned without overlap or clipping.",
      "Form fields and action buttons are visible with consistent spacing.",
      "No section content bleeds outside its container.",
    ],
  },
  {
    id: "remote-file-picker",
    path: "/apps/remote-file-picker",
    assertions: [
      "The upload workflow sections are readable and ordered logically.",
      "No label, helper text, or input field is truncated or hidden.",
      "Buttons and interactive controls are not visually disabled unless intended.",
    ],
  },
  {
    id: "thenv-console",
    path: "/apps/thenv",
    assertions: [
      "Policy and activation sections are visually separated and readable.",
      "Status indicators and metadata text are legible and not overlapping.",
      "The page keeps consistent spacing and alignment across major cards.",
    ],
  },
];

function verifyMidsceneEnvironment(): void {
  const missing = REQUIRED_MIDSCENE_ENV.filter((name) => !process.env[name]);
  if (missing.length > 0) {
    throw new Error(
      [
        `Missing required Midscene environment variables: ${missing.join(", ")}`,
        "Copy apps/devkit/.env.visual-qa.example to apps/devkit/.env.visual-qa and fill in secrets.",
      ].join(" "),
    );
  }
}

test.beforeAll(() => {
  verifyMidsceneEnvironment();
});

for (const scenario of VISUAL_SCENARIOS) {
  test(`visual qa: ${scenario.id}`, async ({ page, aiAssert, aiQuery }, testInfo) => {
    console.info(
      JSON.stringify({
        component: "visual-qa",
        event: "scenario.start",
        scenario: scenario.id,
        path: scenario.path,
      }),
    );

    await page.goto(scenario.path, { waitUntil: "networkidle" });
    await page.waitForTimeout(1_200);

    await aiAssert(
      "There are no severe visual defects like overlapping text, broken alignment, unreadable contrast, or clipped controls.",
    );

    for (const assertion of scenario.assertions) {
      console.info(
        JSON.stringify({
          component: "visual-qa",
          event: "scenario.assertion.start",
          scenario: scenario.id,
          assertion,
        }),
      );
      await aiAssert(assertion);
    }

    const summary = await aiQuery<string>(
      "Summarize this screen for QA in 3 short bullet points focused on visual quality and UX consistency.",
    );

    const summaryText = String(summary);
    await testInfo.attach(`${scenario.id}-summary`, {
      body: Buffer.from(summaryText, "utf-8"),
      contentType: "text/plain",
    });

    console.info(
      JSON.stringify({
        component: "visual-qa",
        event: "scenario.complete",
        scenario: scenario.id,
        summary: summaryText,
      }),
    );
  });
}
