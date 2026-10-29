// HTTP API Lambda authorizer (payload 2.0, simple responses): allows
// requests whose Authorization header is "Bearer <token>".
import { timingSafeEqual } from "node:crypto";
import { GetParameterCommand, SSMClient } from "@aws-sdk/client-ssm";

const ssm = new SSMClient({});
let expected;

const same = (a, b) => {
  const x = Buffer.from(a);
  const y = Buffer.from(b);
  return x.length === y.length && timingSafeEqual(x, y);
};

export const handler = async (event) => {
  if (expected === undefined) {
    const out = await ssm.send(new GetParameterCommand({ Name: process.env.TOKEN_PARAMETER, WithDecryption: true }));
    expected = "Bearer " + out.Parameter.Value;
  }
  const ok = same(event.headers?.authorization ?? "", expected);
  return { isAuthorized: ok, context: { caller: ok ? "api-client" : "anonymous" } };
};
