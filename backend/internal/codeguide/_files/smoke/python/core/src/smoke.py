"""Smoke checks (architecture mode): prove the wiring, hold no business logic.

A check runs only when its env var is set; each one writes and reads back a
unique value. SDKs import lazily, so only the ones in use must be installed.
"""
import os
import time
import uuid

ENV = os.environ


def aws(service, **kwargs):
    """boto3 client; AWS_REGION (set by Lambda and ECS) wins over boto3's AWS_DEFAULT_REGION."""
    import boto3

    return boto3.client(service, region_name=ENV.get("AWS_REGION") or None, **kwargs)


def check_dynamodb(item_id):
    db = aws("dynamodb")
    key = {ENV.get("TABLE_KEY", "id"): {"S": item_id}}
    db.put_item(TableName=ENV["TABLE_NAME"], Item=key)
    return "Item" in db.get_item(TableName=ENV["TABLE_NAME"], Key=key, ConsistentRead=True)


def check_s3(item_id):
    from botocore.config import Config

    # Emulators serve S3 path-style.
    s3 = aws("s3", config=Config(s3={"addressing_style": "path"}) if ENV.get("AWS_ENDPOINT_URL") else None)
    s3.put_object(Bucket=ENV["BUCKET_NAME"], Key=f"smoke/{item_id}", Body=item_id.encode())
    return s3.get_object(Bucket=ENV["BUCKET_NAME"], Key=f"smoke/{item_id}")["Body"].read().decode() == item_id


def check_sqs(item_id):
    sqs, url = aws("sqs"), ENV["QUEUE_URL"]
    sqs.send_message(QueueUrl=url, MessageBody=item_id)
    for _ in range(5):
        for m in sqs.receive_message(QueueUrl=url, MaxNumberOfMessages=10, WaitTimeSeconds=1).get("Messages", []):
            sqs.delete_message(QueueUrl=url, ReceiptHandle=m["ReceiptHandle"])
            if m["Body"] == item_id:
                return True
    return False


def check_sns(item_id):
    return bool(aws("sns").publish(TopicArn=ENV["TOPIC_ARN"], Message=item_id).get("MessageId"))


def check_blob(item_id):
    from azure.storage.blob import BlobServiceClient

    container = BlobServiceClient.from_connection_string(ENV["AZURE_STORAGE_CONNECTION_STRING"]).get_container_client(ENV.get("BLOB_CONTAINER", "smoke"))
    if not container.exists():
        container.create_container()
    blob = container.get_blob_client(f"smoke/{item_id}")
    blob.upload_blob(item_id.encode())
    return blob.download_blob().readall().decode() == item_id


def check_servicebus(item_id):
    from azure.servicebus import ServiceBusClient, ServiceBusMessage, ServiceBusReceiveMode

    with ServiceBusClient.from_connection_string(ENV["SERVICEBUS_CONNECTION_STRING"]) as client:
        with client.get_queue_sender(ENV["SERVICEBUS_QUEUE"]) as sender:
            sender.send_messages(ServiceBusMessage(item_id))
        with client.get_queue_receiver(ENV["SERVICEBUS_QUEUE"], receive_mode=ServiceBusReceiveMode.RECEIVE_AND_DELETE) as receiver:
            return any(str(m) == item_id for m in receiver.receive_messages(max_message_count=10, max_wait_time=5))


def check_gcs(item_id):
    from google.cloud import storage

    blob = storage.Client().bucket(ENV["GCS_BUCKET"]).blob(f"smoke/{item_id}")
    blob.upload_from_string(item_id)
    return blob.download_as_text() == item_id


def check_pubsub(item_id):
    from google.cloud import pubsub_v1

    publisher = pubsub_v1.PublisherClient()
    project = ENV.get("GOOGLE_CLOUD_PROJECT") or ENV["GCP_PROJECT"]
    publisher.publish(publisher.topic_path(project, ENV["PUBSUB_TOPIC"]), item_id.encode()).result(timeout=10)
    if not ENV.get("PUBSUB_SUBSCRIPTION"):
        return True  # publish-only when no subscription is wired
    subscriber = pubsub_v1.SubscriberClient()
    path = subscriber.subscription_path(project, ENV["PUBSUB_SUBSCRIPTION"])
    for _ in range(5):
        msgs = subscriber.pull(subscription=path, max_messages=10, timeout=5).received_messages
        if msgs:
            subscriber.acknowledge(subscription=path, ack_ids=[m.ack_id for m in msgs])
        if any(m.message.data.decode() == item_id for m in msgs):
            return True
    return False


def check_postgres(item_id):
    import psycopg

    with psycopg.connect(ENV["DATABASE_URL"]) as conn:
        conn.execute("CREATE TABLE IF NOT EXISTS smoke (id text PRIMARY KEY)")
        conn.execute("INSERT INTO smoke (id) VALUES (%s)", (item_id,))
        return conn.execute("SELECT id FROM smoke WHERE id = %s", (item_id,)).fetchone() is not None


def check_redis(item_id):
    import redis

    client = redis.Redis.from_url(ENV["REDIS_URL"])
    client.set(f"smoke:{item_id}", item_id, ex=60)
    return client.get(f"smoke:{item_id}") == item_id.encode()


CHECKS = [
    ("dynamodb", "TABLE_NAME", check_dynamodb),
    ("s3", "BUCKET_NAME", check_s3),
    ("sqs", "QUEUE_URL", check_sqs),
    ("sns", "TOPIC_ARN", check_sns),
    ("blob", "AZURE_STORAGE_CONNECTION_STRING", check_blob),
    ("servicebus", "SERVICEBUS_CONNECTION_STRING", check_servicebus),
    ("gcs", "GCS_BUCKET", check_gcs),
    ("pubsub", "PUBSUB_TOPIC", check_pubsub),
    ("postgres", "DATABASE_URL", check_postgres),
    ("redis", "REDIS_URL", check_redis),
]


def smoke(path, log):
    """Answers GET <prefix>/health and GET <prefix>/smoke as (status, body).

    Entry adapters translate their platform's request/response to this shape.
    """
    if path.endswith("/health"):
        return 200, {"status": "ok"}
    if not path.endswith("/smoke"):
        return 404, {"error": "not found"}
    item_id = f"smoke-{int(time.time() * 1000)}-{uuid.uuid4().hex[:6]}"
    results = {}
    for name, var, check in CHECKS:
        if not ENV.get(var):
            continue
        try:
            results[name] = "ok" if check(item_id) else "mismatch"
        except Exception as err:  # report every store, never stop at the first failure
            results[name] = "error"
            log({"level": "error", "check": name, "error": type(err).__name__, "message": str(err)})
    ok = all(r == "ok" for r in results.values())
    log({"level": "info" if ok else "error", "msg": "smoke", "ok": ok, "results": results})
    return (200 if ok else 500), {"ok": ok, "results": results}
