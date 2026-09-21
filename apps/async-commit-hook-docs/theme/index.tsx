import {
  Layout as BasicLayout,
  type LayoutProps,
} from "@rspress/core/theme-original";
import { DocumentationSiteId, DocsSiteSwitcher } from "../../../packages/docs-site-switcher/src/index";

function Layout(props: LayoutProps) {
  return (
    <BasicLayout
      {...props}
      beforeNavTitle={
        <>
          <DocsSiteSwitcher currentSite={DocumentationSiteId.AsyncCommitHook} />
          {props.beforeNavTitle}
        </>
      }
    />
  );
}

export { Layout };
export * from "@rspress/core/theme-original";
