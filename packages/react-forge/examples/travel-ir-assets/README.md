# ROAM investor presentation

`../travel-ir.tsx` is a complete, English, 12-slide investor presentation for **ROAM**, a fictional collaborative travel app. It demonstrates React Forge's native PowerPoint text, images, editable table, and editable chart with embedded workbook data. It is an example investment case, not an actual company, investment offer, product launch, customer study, or financial forecast.

## Generate the presentation

From the repository root on Node.js 24 and any supported macOS, Windows or glibc Linux x64/arm64 host (replace the output with a native writable path on Windows):

```sh
pnpm --filter @delino/react-forge build
pnpm --filter @delino/react-forge cli run examples/travel-ir.tsx --output /tmp/roam-investor-deck.pptx --json
```

The CLI rejects an existing output unless `--overwrite` is supplied. The task resolves assets relative to its own module, registers the three images concurrently with the caller's abort signal, and disposes its session if authoring fails. Normal CLI completion also disposes the returned session. No network access, image generation, or presentation converter is required to run the example.

The authoring canvas is 960 × 540 points (16:9). Slide text selects Avenir Next on macOS, Segoe UI on Windows, and Noto Sans on Linux (install `fonts-noto-core` on Ubuntu). Text frames permit at most 15% font reduction with a 10-point caption floor, and table rows accommodate two lines, to handle host font metrics without clipping. The native chart retains Forge's Calibri label style. Fonts are referenced, not redistributed, and Office hosts may substitute unavailable fonts. The six-host CI matrix generates the complete example on each host; the original macOS visual review is not a claim of identical typography on other systems. The interface mockup and travel imagery are raster assets; surrounding text, the acquisition table on slide 7, and the bookings chart on slide 8 remain editable Office objects. The mockup is not an implemented app screen.

## Story and reusable components

1. Cover: less planning, more going.
2. Problem: fragmented planning and group coordination hypotheses.
3. Product: the proposed shared itinerary app.
4. Experience: save, plan together, and book.
5. Market: sourced global context and a separately labeled cohort scenario.
6. Economics: illustrative commission and contribution assumptions.
7. Distribution: three acquisition experiments in an editable table.
8. Milestones: quarterly booking targets in a native chart.
9. Advantage: the proposed value of group context and repeat use.
10. Roadmap: staged learning and explicit risks.
11. Fundraise: an illustrative $1.5M budget and 18-month runway.
12. Closing: make room for the trip itself.

`Copy` centralizes native text styling and rejects overflow instead of silently shrinking text. `Page` supplies the shared canvas, heading and page marker. Each slide is a normal reusable React component. The `assumptions` object is the single source for financial calculations, the chart series and allocation amounts. Edit that object and the relevant narrative together when adapting the example; regenerate and visually review all slides after changes.

## Facts and assumptions

The sole external market statistic is **1.52 billion international tourist arrivals in 2025**, an estimate published by UN Tourism on 20 January 2026 in its January 2026 World Tourism Barometer news release. Arrivals count trips, not unique travelers. The figure is market context, not ROAM's TAM or a customer count. [Primary UN Tourism source](https://pre-webunwto.s3.eu-west-1.amazonaws.com/s3fs-public/2026-01/260120-international-tourist-arrivals-up-4-in-2025-reflecting-strong-travel-demand-around-the-world-en.pdf?VersionId=AAlwg.p32bsTz1udNuQw1gLxOuZnwm1H), checked 24 September 2026. Slide 5 carries the publication name and date.

Every company-specific metric is an assumption or target:

| Model | Calculation |
| --- | --- |
| Revenue per booked trip | $800 commissionable trip value × 8% = $64 |
| Contribution per booked trip | $64 revenue − $14 variable costs = $50 |
| Cohort opportunity scenario | 200,000 travelers × 1.2 trips/year × $64 = $15.36M/year |
| First-year bookings target | 500 + 1,500 + 3,000 + 5,000 = 10,000 |
| First-year net revenue target | 10,000 bookings × $64 = $640,000 |
| Acquisition target | $25 per first-time booker, blended across channels |
| Repeat target | 25% second-booking rate within 90 days; Q4 customers with a complete observation window |
| Fundraise allocation | $750K product/engineering + $375K demand + $225K supply/operations + $150K contingency = $1.5M |
| Runway scenario | $1.5M ÷ 18 months ≈ $83K average monthly cash use |

All currency figures are USD. Commission is assumed net of cancellations; the scenario implies no secured partner terms. Lisbon and Barcelona are proposed pilot destinations. No actual users, bookings, revenues, team credentials, interviews, partners, or customer testimonials are claimed. The launch cohort is an arithmetic scenario rather than a sourced estimate of the addressable market.

## Image provenance

The three original images were generated with the built-in `image_gen` tool on 24 September 2026 specifically for this example. They are not documentary destination photographs, customer material, or evidence of a shipping product. Exact prompts are retained in `prompts.json`. No fonts or third-party stock photographs are included.

| File | Role |
| --- | --- |
| `coast.png` | Cover's Mediterranean travel concept |
| `product.png` | Proposed ROAM itinerary interface mockup |
| `horizon.png` | Closing slide's train-window travel concept |

These generated assets are included as repository example assets under the repository's MIT license. Generating the presentation reuses the committed files and does not invoke an image service.

## Validation boundary

Check the example's types after building the package:

```sh
pnpm --filter @delino/react-forge exec tsc --noEmit --target ES2023 --module NodeNext --moduleResolution NodeNext --jsx react-jsx --strict --skipLibCheck examples/travel-ir.tsx
```

The delivered deck was structurally checked for 12 slides, its editable table, native chart/workbook data and slide geometry, then rendered with test-only LibreOffice and Poppler and visually reviewed slide by slide. The compact record is `../../tests/evidence/travel-ir-macos-arm64.json`; that renderer substituted Carlito for the chart's Calibri while using Avenir Next for slide text. Office applications remain authoritative for final rendering. This does not claim a direct Microsoft PowerPoint or Google Slides test. Generated PPTX/PDF/PNG previews are local outputs, not tracked source or runtime dependencies.
