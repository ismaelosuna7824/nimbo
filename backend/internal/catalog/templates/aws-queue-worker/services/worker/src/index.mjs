export const handler = async (event) => {
  const batchItemFailures = [];
  for (const record of event.Records ?? []) {
    try {
      const job = JSON.parse(record.body);
      console.log("processing job", record.messageId, job);
    } catch (err) {
      console.error("invalid job", record.messageId, err);
      batchItemFailures.push({ itemIdentifier: record.messageId });
    }
  }
  return { batchItemFailures };
};
