import {
  DocFooter as BasicDocFooter,
  Layout as BasicLayout,
  type LayoutProps,
} from "@rspress/core/theme-original";
import { DocumentationSiteId, DocsSiteSwitcher } from "../../../packages/docs-site-switcher/src/index";

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

function Layout(props: LayoutProps) {
  return (
    <BasicLayout
      {...props}
      beforeNavTitle={
        <>
          <DocsSiteSwitcher currentSite={DocumentationSiteId.Runmoor} />
          {props.beforeNavTitle}
        </>
      }
    />
  );
}

export { DocFooter, Layout };
export * from "@rspress/core/theme-original";
