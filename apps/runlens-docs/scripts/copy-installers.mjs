import { copyFileSync, mkdirSync } from "node:fs";
mkdirSync(new URL("../docs/public/", import.meta.url), { recursive: true });
for (const [source, output] of [["runlens.sh", "install.sh"], ["runlens.ps1", "install.ps1"]]) {
  copyFileSync(new URL(`../../../scripts/install/${source}`, import.meta.url), new URL(`../docs/public/${output}`, import.meta.url));
}

mkdirSync(new URL("../docs/public/schema/", import.meta.url), { recursive: true });
for (const name of ["config-v1.json", "report-v1.json"]) {
  copyFileSync(new URL(`../../../crates/runlens/schema/${name}`, import.meta.url), new URL(`../docs/public/schema/${name}`, import.meta.url));
}
