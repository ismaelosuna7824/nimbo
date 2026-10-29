"""AWS Lambda entry (API Gateway REST or HTTP API). boto3 ships with the
Python runtimes: src/ deploys as-is."""
import json

from smoke import smoke


def handler(event, context):
    def log(record):
        print(json.dumps({"requestId": getattr(context, "aws_request_id", None), **record}))

    status, body = smoke(event.get("rawPath") or event.get("path") or "/", log)
    return {"statusCode": status, "headers": {"content-type": "application/json"}, "body": json.dumps(body)}
