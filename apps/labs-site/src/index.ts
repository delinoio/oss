// SPDX-License-Identifier: Apache-2.0
import "./styles.css";

// Native details keeps language navigation usable without JavaScript. Escape
// and outside clicks provide the same dismissal behavior as other site menus.
const languages = document.querySelector<HTMLDetailsElement>(".languages");
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && languages?.open) {
    languages.open = false;
    languages.querySelector("summary")?.focus();
  }
});
document.addEventListener("click", (event) => {
  if (
    languages?.open &&
    event.target instanceof Node &&
    !languages.contains(event.target)
  ) {
    languages.open = false;
  }
});
