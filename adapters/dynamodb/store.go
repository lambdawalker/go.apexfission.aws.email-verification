// Package dynamodb implements aggregate CAS storage using AWS SDK v2.
package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
	"strconv"
)

type API interface {
	GetItem(context.Context, *ddb.GetItemInput, ...func(*ddb.Options)) (*ddb.GetItemOutput, error)
	PutItem(context.Context, *ddb.PutItemInput, ...func(*ddb.Options)) (*ddb.PutItemOutput, error)
}
type Store struct {
	Client API
	Table  string
}

func (s Store) Load(ctx context.Context, k string) (storage.Record, error) {
	if s.Client == nil || s.Table == "" {
		return storage.Record{}, fmt.Errorf("DynamoDB client and table required")
	}
	o, e := s.Client.GetItem(ctx, &ddb.GetItemInput{TableName: aws.String(s.Table), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: k}}})
	if e != nil {
		return storage.Record{}, e
	}
	if len(o.Item) == 0 {
		return storage.Record{}, storage.ErrMissing
	}
	v, ok := o.Item["version"].(*types.AttributeValueMemberN)
	if !ok {
		return storage.Record{}, errors.New("invalid stored version")
	}
	n, e := strconv.ParseUint(v.Value, 10, 64)
	b, ok := o.Item["data"].(*types.AttributeValueMemberB)
	if e != nil || !ok || n == 0 {
		return storage.Record{}, errors.New("corrupt aggregate")
	}
	return storage.Record{Version: n, Data: append([]byte(nil), b.Value...)}, nil
}
func (s Store) CompareAndSwap(ctx context.Context, k string, v uint64, b []byte) error {
	if s.Client == nil || s.Table == "" || v == ^uint64(0) || len(b) > 300000 {
		return errors.New("invalid DynamoDB write")
	}
	condition := "attribute_not_exists(pk)"
	var values map[string]types.AttributeValue
	if v != 0 {
		condition = "#v = :expected"
		values = map[string]types.AttributeValue{":expected": &types.AttributeValueMemberN{Value: strconv.FormatUint(v, 10)}}
	}
	in := &ddb.PutItemInput{TableName: aws.String(s.Table), Item: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: k}, "version": &types.AttributeValueMemberN{Value: strconv.FormatUint(v+1, 10)}, "data": &types.AttributeValueMemberB{Value: b}}, ConditionExpression: aws.String(condition), ExpressionAttributeValues: values}
	if v != 0 {
		in.ExpressionAttributeNames = map[string]string{"#v": "version"}
	}
	_, e := s.Client.PutItem(ctx, in)
	var conflict *types.ConditionalCheckFailedException
	if errors.As(e, &conflict) {
		return storage.ErrConflict
	}
	return e
}
