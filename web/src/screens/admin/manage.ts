import { queryOptions } from "@tanstack/react-query";
import type { AppsPage, ClassesPage, PresetsPage, ProfilesPage, UsagePage } from "../../gen/panel";
import { api } from "../../lib/api";

// What the classes, Apps, bases, profiles and usage screens ask the server. They sit under
// "admin" like the others, so a mutation invalidates them with one key.
export const classesQuery = queryOptions({
  queryKey: ["admin", "classes"],
  queryFn: () => api<ClassesPage>("GET", "/api/classes"),
});

export const appsQuery = queryOptions({
  queryKey: ["admin", "apps"],
  queryFn: () => api<AppsPage>("GET", "/api/github/apps"),
});

export const presetsQuery = queryOptions({
  queryKey: ["admin", "presets"],
  queryFn: () => api<PresetsPage>("GET", "/api/presets"),
});

export const profilesQuery = queryOptions({
  queryKey: ["admin", "profiles"],
  queryFn: () => api<ProfilesPage>("GET", "/api/profiles"),
});

export const usageQuery = (days: number) =>
  queryOptions({
    queryKey: ["admin", "usage", days],
    queryFn: () => api<UsagePage>("GET", `/api/usage?days=${days}`),
  });

/** A text of the catalog by a key built at run time; a key the catalog lacks falls back to the raw value. */
export type Say = (k: string, o?: object) => string;

export function labelOr(say: Say, key: string, raw: string): string {
  const text = say(key);
  return text === key || raw === "" ? raw : text;
}
