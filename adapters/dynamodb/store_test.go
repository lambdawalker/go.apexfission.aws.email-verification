package dynamodb

import (
	"context"
	"errors"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
	"testing"
)

type fake struct {
	get  *dynamodb.GetItemInput
	put  *dynamodb.PutItemInput
	err  error
	item map[string]types.AttributeValue
}

func (f *fake) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.get = in
	return &dynamodb.GetItemOutput{Item: f.item}, f.err
}
func (f *fake) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.put = in
	return &dynamodb.PutItemOutput{}, f.err
}
func TestConsistencyAndConditions(t *testing.T) {
	f := &fake{}
	s := Store{f, "table"}
	ctx := context.Background()
	if _, e := s.Load(ctx, "key"); !errors.Is(e, storage.ErrMissing) {
		t.Fatal(e)
	}
	if f.get.ConsistentRead == nil || !*f.get.ConsistentRead {
		t.Fatal("eventual read")
	}
	if e := s.CompareAndSwap(ctx, "key", 0, []byte("{}")); e != nil {
		t.Fatal(e)
	}
	if *f.put.ConditionExpression != "attribute_not_exists(pk)" {
		t.Fatal("unguarded creation")
	}
	if e := s.CompareAndSwap(ctx, "key", 3, []byte("{}")); e != nil {
		t.Fatal(e)
	}
	if f.put.ExpressionAttributeValues[":expected"].(*types.AttributeValueMemberN).Value != "3" {
		t.Fatal("wrong version")
	}
	f.err = &types.ConditionalCheckFailedException{}
	if e := s.CompareAndSwap(ctx, "key", 3, nil); !errors.Is(e, storage.ErrConflict) {
		t.Fatal(e)
	}
	f.err = errors.New("unavailable")
	if _, e := s.Load(ctx, "key"); errors.Is(e, storage.ErrMissing) || e == nil {
		t.Fatal("outage became absence")
	}
}
