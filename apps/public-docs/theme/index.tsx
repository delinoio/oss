import { usePageData } from "@rspress/core/runtime";
import {
  Layout as BasicLayout,
  type LayoutProps,
} from "@rspress/core/theme-original";
import {
  DocsProductId,
  DocsProductSwitcher,
} from "@delinoio/docs-site-switcher";
import "./repository-footer.css";

function routePathFromPageData(page: unknown) {
  if (typeof page !== "object" || page === null || !("routePath" in page)) {
    return "";
  }

  const routePath = page.routePath;
  return typeof routePath === "string" ? routePath : "";
}

function PublicDocsProductSwitcher() {
  const { page } = usePageData();
  const routePath = routePathFromPageData(page);
  const currentProduct = routePath === "/runmoor" || routePath.startsWith("/runmoor/")
    ? DocsProductId.Runmoor
    : DocsProductId.PublicDocs;

  return <DocsProductSwitcher currentProduct={currentProduct} />;
}

function RepositoryFooter() {
  return (
    <footer className="delino-repository-footer">
      <a href="https://github.com/delinoio/oss">
        View this documentation on GitHub
      </a>
    </footer>
  );
}

function Layout(props: LayoutProps) {
  return (
    <BasicLayout
      {...props}
      beforeNavTitle={
        <>
          <PublicDocsProductSwitcher />
          {props.beforeNavTitle}
        </>
      }
      afterDocFooter={
        <>
          <RepositoryFooter />
          {props.afterDocFooter}
        </>
      }
    />
  );
}

export { Layout };
export * from "@rspress/core/theme-original";
