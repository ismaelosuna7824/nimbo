"""Driven adapter: DynamoDB table with string hash key "id"."""
from datetime import datetime

import boto3

from app.domain import DomainError, Item, ItemName


class DynamoItemRepository:
    def __init__(self, table: str, region: str | None = None):
        self._db = boto3.client("dynamodb", region_name=region)
        self._table = table

    def save(self, item: Item) -> None:
        try:
            self._db.put_item(
                TableName=self._table,
                Item={"id": {"S": item.id}, "name": {"S": item.name.value}, "createdAt": {"S": item.created_at.isoformat()}},
                ConditionExpression="attribute_not_exists(id)",
            )
        except self._db.exceptions.ConditionalCheckFailedException:
            raise DomainError("conflict", "item already exists") from None

    def get(self, item_id: str):
        row = self._db.get_item(TableName=self._table, Key={"id": {"S": item_id}}, ConsistentRead=True).get("Item")
        if not row:
            return None
        return Item(item_id, ItemName(row["name"]["S"]), datetime.fromisoformat(row["createdAt"]["S"]))


def create_repository(env) -> DynamoItemRepository:
    """Builds the repository from the env the composition root passes in."""
    if not env.get("TABLE_NAME"):
        raise RuntimeError("TABLE_NAME is not set")
    # AWS_REGION (set by Lambda and ECS) wins over boto3's AWS_DEFAULT_REGION.
    return DynamoItemRepository(env["TABLE_NAME"], env.get("AWS_REGION") or None)
