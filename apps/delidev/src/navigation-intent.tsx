// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext } from "react";

/**
 * Closed set of user-driven handlers that change the surface, the selection, the
 * tab or open a new-item, editor, review or project-creation destination. Each
 * one registers a navigation intent synchronously, before it changes any state,
 * so a pending product-route restore can never override the choice. Controls that
 * only retry, reconnect, refresh, dismiss or copy are not navigation and must not
 * register an intent.
 */
export enum NavigationIntentSource {
  Surface = "surface",
  SessionOpen = "session-open",
  SessionTab = "session-tab",
  SearchResult = "search-result",
  ProjectCreation = "project-creation",
  InboxSelect = "inbox-select",
  InboxClose = "inbox-close",
  ScheduleSelect = "schedule-select",
  ScheduleNew = "schedule-new",
  ScheduleEdit = "schedule-edit",
  ScheduleClose = "schedule-close",
  RepositorySelect = "repository-select",
  SettingsCategory = "settings-category",
  UsageTab = "usage-tab",
}

export type NavigationIntent = (source: NavigationIntentSource) => void;

const NavigationIntentContext = createContext<NavigationIntent>(() => {});

/** `Shell` provides the one navigation-intent token; outside it intents are no-ops. */
export const NavigationIntentProvider = NavigationIntentContext.Provider;

/** Returns the callback child screens call from their navigation handlers. */
export function useNavigationIntent(): NavigationIntent {
  return useContext(NavigationIntentContext);
}
