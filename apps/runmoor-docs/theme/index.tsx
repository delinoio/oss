import { DocFooter as BasicDocFooter } from "@rspress/core/theme-original";

import "./repository-footer.css";

function DocFooter() {
  return (
    <>
      <BasicDocFooter />
      <footer className="delino-repository-footer">
        <a href="https://github.com/delinoio/oss">
          Delino OSS repository
        </a>
      </footer>
    </>
  );
}

export { DocFooter };
export * from "@rspress/core/theme-original";
