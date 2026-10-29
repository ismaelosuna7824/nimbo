// Package store holds the driven adapters of ItemRepository.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"example.com/api/internal/application"
	"example.com/api/internal/domain"
)

// DynamoDB stores items in a table with string hash key "id".
type DynamoDB struct {
	db    *dynamodb.Client
	table string
}

// New builds the repository from the env the composition root passes in.
func New(ctx context.Context, getenv func(string) string) (application.ItemRepository, error) {
	table := getenv("TABLE_NAME")
	if table == "" {
		return nil, errors.New("TABLE_NAME is not set")
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &DynamoDB{dynamodb.NewFromConfig(cfg), table}, nil
}

func (d *DynamoDB) Save(ctx context.Context, item domain.Item) error {
	_, err := d.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(d.table),
		Item: map[string]types.AttributeValue{
			"id":        &types.AttributeValueMemberS{Value: item.ID},
			"name":      &types.AttributeValueMemberS{Value: item.Name.String()},
			"createdAt": &types.AttributeValueMemberS{Value: item.CreatedAt.Format(time.RFC3339Nano)},
		},
		ConditionExpression: aws.String("attribute_not_exists(id)"),
	})
	var dup *types.ConditionalCheckFailedException
	if errors.As(err, &dup) {
		return domain.ErrConflict
	}
	return err
}

func (d *DynamoDB) Get(ctx context.Context, id string) (domain.Item, error) {
	out, err := d.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(d.table),
		Key:            map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return domain.Item{}, err
	}
	if out.Item == nil {
		return domain.Item{}, domain.ErrNotFound
	}
	name, _ := out.Item["name"].(*types.AttributeValueMemberS)
	created, _ := out.Item["createdAt"].(*types.AttributeValueMemberS)
	if name == nil || created == nil {
		return domain.Item{}, errors.New("item " + id + ": malformed row")
	}
	n, err := domain.NewItemName(name.Value)
	if err != nil {
		return domain.Item{}, err
	}
	at, err := time.Parse(time.RFC3339Nano, created.Value)
	return domain.Item{ID: id, Name: n, CreatedAt: at}, err
}
