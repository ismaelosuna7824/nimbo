// AWS Lambda entry (API Gateway REST or HTTP API). The nodejs18.x+ runtimes
// ship the AWS SDK v3: src/ deploys as-is.
import { smoke } from "./smoke.mjs";

export const handler = async (event, context) => {
  const log = (o) => console.log(JSON.stringify({ requestId: context?.awsRequestId, ...o }));
  const res = await smoke(event.rawPath ?? event.path ?? "/", log);
  return { statusCode: res.status, headers: { "content-type": "application/json" }, body: JSON.stringify(res.body) };
};
