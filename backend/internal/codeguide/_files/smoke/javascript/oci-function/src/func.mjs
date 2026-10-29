// OCI Functions entry (Fn FDK) behind OCI API Gateway.
import fdk from "@fnproject/fdk";
import { smoke } from "./smoke.mjs";

fdk.handle(async (_input, ctx) => {
  const gw = ctx.httpGateway;
  const out = await smoke(new URL(gw.requestURL, "http://gateway").pathname, (o) => console.log(JSON.stringify({ callId: ctx.callID, ...o })));
  gw.statusCode = out.status;
  return out.body;
}, { inputMode: "string" });
