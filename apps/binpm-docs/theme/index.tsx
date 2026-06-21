import { DocFooter as BasicDocFooter } from "@rspress/core/theme-original";

import "./repository-footer.css";

function DocFooter() {
  return (
    <>
      <BasicDocFooter />
      <footer className="delino-repository-footer">
        <a
          className="delino-repository-footer__link"
          href="https://github.com/delinoio/oss"
          rel="noreferrer"
          target="_blank"
        >
          Delino OSS repository
        </a>
      </footer>
    </>
  );
}

export { DocFooter };
export * from "@rspress/core/theme-original";
