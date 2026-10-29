// Cloud Functions (2nd gen) entry; entry point "smoke".
import { http } from "@google-cloud/functions-framework";
import { smoke } from "./smoke.mjs";

http("smoke", async (req, res) => {
  const trace = req.get("x-cloud-trace-context");
  const out = await smoke(req.path, (o) => console.log(JSON.stringify({ trace, ...o })));
  res.status(out.status).json(out.body);
});
