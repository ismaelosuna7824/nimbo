// Azure Functions entry (Node.js programming model v4): /api/health, /api/smoke.
import { app } from "@azure/functions";
import { smoke } from "../smoke.mjs";

app.http("smoke", {
  methods: ["GET"],
  authLevel: "anonymous",
  route: "{*path}",
  handler: async (request, context) => {
    const res = await smoke(new URL(request.url).pathname, (o) => context.log(JSON.stringify({ invocationId: context.invocationId, ...o })));
    return { status: res.status, jsonBody: res.body };
  },
});
