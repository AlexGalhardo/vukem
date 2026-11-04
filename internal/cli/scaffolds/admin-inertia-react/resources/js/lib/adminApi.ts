// Types of the framework panel's JSON presenter (Accept: application/json on its dashboard, list and detail routes).
export interface NavItem {
  label: string;
  url: string;
  /** "resource" has JSON list and detail routes; "page" is a custom page served as HTML by the panel. */
  kind: "resource" | "page";
  active: boolean;
}

export interface PanelData {
  title: string;
  home: string;
  auditUrl?: string;
  flagsUrl?: string;
  nav: NavItem[] | null;
}

export interface DashboardData {
  panel: PanelData;
  cards: { label: string; url: string; count: number }[] | null;
}

export interface ListData {
  resource: { singular: string; plural: string; url: string; createUrl?: string };
  columns: { label: string; sortUrl?: string; sorted?: string }[] | null;
  rows: { id: string; cells: string[]; showUrl?: string }[] | null;
  search: string;
  searchable: boolean;
  sort: string;
  dir: string;
  total: number;
  pager: { page: number; last: number };
}

export interface RecordData {
  resource: { singular: string; plural: string };
  id: string;
  fields: { label: string; value: string }[] | null;
}

export async function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin", signal });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return (await res.json()) as T;
}
