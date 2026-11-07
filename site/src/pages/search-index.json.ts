import type { APIRoute } from "astro";
import { buildSearchIndex } from "@/lib/search";

export const prerender = true;

export const GET: APIRoute = () => Response.json(buildSearchIndex());
