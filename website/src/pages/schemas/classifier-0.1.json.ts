import type { APIRoute } from "astro";
import schema from "../../../../schemas/classifier-0.1.json";

// Publish the generator's source of truth without maintaining a second copy.
export const GET: APIRoute = () =>
  new Response(JSON.stringify(schema, null, 2) + "\n", {
    headers: { "Content-Type": "application/json" },
  });
