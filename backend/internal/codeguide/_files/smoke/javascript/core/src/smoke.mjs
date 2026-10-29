// Smoke checks (architecture mode): prove the wiring, hold no business logic.
// A check runs only when its env var is set; each one writes and reads back a
// unique value. SDKs load lazily, so only the ones in use must be installed.
const env = process.env;

const checks = [
  ["dynamodb", "TABLE_NAME", async (id) => {
    const { DynamoDBClient, PutItemCommand, GetItemCommand } = await import("@aws-sdk/client-dynamodb");
    const db = new DynamoDBClient({});
    const key = { [env.TABLE_KEY || "id"]: { S: id } };
    await db.send(new PutItemCommand({ TableName: env.TABLE_NAME, Item: key }));
    return Boolean((await db.send(new GetItemCommand({ TableName: env.TABLE_NAME, Key: key, ConsistentRead: true }))).Item);
  }],
  ["s3", "BUCKET_NAME", async (id) => {
    const { S3Client, PutObjectCommand, GetObjectCommand } = await import("@aws-sdk/client-s3");
    const s3 = new S3Client({ forcePathStyle: Boolean(env.AWS_ENDPOINT_URL) }); // emulators serve path-style
    await s3.send(new PutObjectCommand({ Bucket: env.BUCKET_NAME, Key: `smoke/${id}`, Body: id }));
    const got = await s3.send(new GetObjectCommand({ Bucket: env.BUCKET_NAME, Key: `smoke/${id}` }));
    return (await got.Body.transformToString()) === id;
  }],
  ["sqs", "QUEUE_URL", async (id) => {
    const { SQSClient, SendMessageCommand, ReceiveMessageCommand, DeleteMessageCommand } = await import("@aws-sdk/client-sqs");
    // Resolve the endpoint from the SDK config, not the queue URL's host (emulators).
    const sqs = new SQSClient({ useQueueUrlAsEndpoint: false });
    await sqs.send(new SendMessageCommand({ QueueUrl: env.QUEUE_URL, MessageBody: id }));
    for (let i = 0; i < 5; i++) {
      const { Messages = [] } = await sqs.send(new ReceiveMessageCommand({ QueueUrl: env.QUEUE_URL, MaxNumberOfMessages: 10, WaitTimeSeconds: 1 }));
      for (const m of Messages) {
        await sqs.send(new DeleteMessageCommand({ QueueUrl: env.QUEUE_URL, ReceiptHandle: m.ReceiptHandle }));
        if (m.Body === id) return true;
      }
    }
    return false;
  }],
  ["sns", "TOPIC_ARN", async (id) => {
    const { SNSClient, PublishCommand } = await import("@aws-sdk/client-sns");
    return Boolean((await new SNSClient({}).send(new PublishCommand({ TopicArn: env.TOPIC_ARN, Message: id }))).MessageId);
  }],
  ["blob", "AZURE_STORAGE_CONNECTION_STRING", async (id) => {
    const { BlobServiceClient } = await import("@azure/storage-blob");
    const container = BlobServiceClient.fromConnectionString(env.AZURE_STORAGE_CONNECTION_STRING).getContainerClient(env.BLOB_CONTAINER || "smoke");
    await container.createIfNotExists();
    const blob = container.getBlockBlobClient(`smoke/${id}`);
    await blob.upload(id, id.length);
    return (await blob.downloadToBuffer()).toString() === id;
  }],
  ["servicebus", "SERVICEBUS_CONNECTION_STRING", async (id) => {
    const { ServiceBusClient } = await import("@azure/service-bus");
    const client = new ServiceBusClient(env.SERVICEBUS_CONNECTION_STRING);
    try {
      await client.createSender(env.SERVICEBUS_QUEUE).sendMessages({ body: id });
      const received = await client.createReceiver(env.SERVICEBUS_QUEUE, { receiveMode: "receiveAndDelete" }).receiveMessages(10, { maxWaitTimeInMs: 5000 });
      return received.some((m) => m.body === id);
    } finally {
      await client.close();
    }
  }],
  ["gcs", "GCS_BUCKET", async (id) => {
    const { Storage } = await import("@google-cloud/storage");
    const file = new Storage().bucket(env.GCS_BUCKET).file(`smoke/${id}`);
    await file.save(id);
    return (await file.download())[0].toString() === id;
  }],
  ["pubsub", "PUBSUB_TOPIC", async (id) => {
    const { PubSub, v1 } = await import("@google-cloud/pubsub");
    const pubsub = new PubSub();
    await pubsub.topic(env.PUBSUB_TOPIC).publishMessage({ data: Buffer.from(id) });
    if (!env.PUBSUB_SUBSCRIPTION) return true; // publish-only when no subscription is wired
    const subscriber = new v1.SubscriberClient();
    const subscription = subscriber.subscriptionPath(await pubsub.getProjectId(), env.PUBSUB_SUBSCRIPTION);
    for (let i = 0; i < 5; i++) {
      const [res] = await subscriber.pull({ subscription, maxMessages: 10 });
      const msgs = res.receivedMessages ?? [];
      if (msgs.length) await subscriber.acknowledge({ subscription, ackIds: msgs.map((m) => m.ackId) });
      if (msgs.some((m) => m.message.data.toString() === id)) return true;
    }
    return false;
  }],
  ["postgres", "DATABASE_URL", async (id) => {
    const { default: pg } = await import("pg");
    const client = new pg.Client({ connectionString: env.DATABASE_URL });
    await client.connect();
    try {
      await client.query("CREATE TABLE IF NOT EXISTS smoke (id text PRIMARY KEY)");
      await client.query("INSERT INTO smoke (id) VALUES ($1)", [id]);
      return (await client.query("SELECT id FROM smoke WHERE id = $1", [id])).rowCount === 1;
    } finally {
      await client.end();
    }
  }],
  ["redis", "REDIS_URL", async (id) => {
    const { createClient } = await import("redis");
    const client = await createClient({ url: env.REDIS_URL }).connect();
    try {
      await client.set(`smoke:${id}`, id, { EX: 60 });
      return (await client.get(`smoke:${id}`)) === id;
    } finally {
      await client.quit();
    }
  }],
];

// smoke answers GET <prefix>/health and GET <prefix>/smoke; entry adapters
// translate their platform's request/response to this shape.
export async function smoke(path, log) {
  if (path.endsWith("/health")) return { status: 200, body: { status: "ok" } };
  if (!path.endsWith("/smoke")) return { status: 404, body: { error: "not found" } };
  const id = `smoke-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const results = {};
  for (const [name, variable, run] of checks) {
    if (!env[variable]) continue;
    try {
      results[name] = (await run(id)) ? "ok" : "mismatch";
    } catch (err) {
      results[name] = "error";
      log({ level: "error", check: name, error: err.name, message: err.message });
    }
  }
  const ok = Object.values(results).every((r) => r === "ok");
  log({ level: ok ? "info" : "error", msg: "smoke", ok, results });
  return { status: ok ? 200 : 500, body: { ok, results } };
}
