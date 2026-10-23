"""Consumes jobs from RabbitMQ and processes them one at a time."""

import json
import os
import pathlib
import time

import pika

AMQP_URL = os.environ["AMQP_URL"]
QUEUE = os.environ.get("QUEUE", "jobs")
READY = pathlib.Path("/tmp/worker-ready")


def handle(channel, method, _properties, body):
    job = json.loads(body)
    print(f"worker: processing job {job['id']} ({job.get('task')})", flush=True)
    time.sleep(0.2)  # the work
    channel.basic_ack(delivery_tag=method.delivery_tag)
    print(f"worker: done {job['id']}", flush=True)


def main():
    while True:
        try:
            connection = pika.BlockingConnection(pika.URLParameters(AMQP_URL))
            channel = connection.channel()
            channel.queue_declare(queue=QUEUE, durable=True)
            channel.basic_qos(prefetch_count=1)
            channel.basic_consume(queue=QUEUE, on_message_callback=handle)
            READY.touch()
            print(f"worker: waiting for jobs on {QUEUE}", flush=True)
            channel.start_consuming()
        except pika.exceptions.AMQPConnectionError as err:
            READY.unlink(missing_ok=True)
            print(f"worker: broker unavailable ({err}), retrying", flush=True)
            time.sleep(2)


if __name__ == "__main__":
    main()
