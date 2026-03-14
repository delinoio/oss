import { Sidebar } from "./components/sidebar";
import { TabBar } from "./components/tab-bar";
import { AppRoutes } from "./routes";

export function App() {
  return (
    <div className="flex h-full">
      <Sidebar />
      <div className="flex flex-col flex-1 min-w-0">
        <TabBar />
        <main className="flex-1 overflow-hidden">
          <AppRoutes />
        </main>
      </div>
    </div>
  );
}
