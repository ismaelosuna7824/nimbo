// One service behind the gateway; every service function runs this code
// with its own SERVICE_NAME and SERVICE_RECORDS (JSON). The gateway already
// checked the caller and passes who it is in the authorizer context.
const service = process.env.SERVICE_NAME;
const records = JSON.parse(process.env.SERVICE_RECORDS ?? "[]");

export const handler = async (event) => ({
  statusCode: 200,
  headers: { "content-type": "application/json" },
  body: JSON.stringify({
    service,
    caller: event.requestContext?.authorizer?.lambda?.caller ?? null,
    items: records,
  }),
});
