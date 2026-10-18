exports.handler = async (event) => ({ statusCode: 200, body: JSON.stringify({ ok: true, n: event.Records?.length ?? 0 }) })
