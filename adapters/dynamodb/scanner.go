package dynamodb

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dt "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/worker"
	"strings"
)

// Scanner is a bounded, resumable scan source for low-volume installations.
// Large workloads should supply a worker.Source backed by a durable work index.
type ScanAPI interface {
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}
type Scanner struct {
	Client ScanAPI
	Table  string
}

func (s Scanner) Scan(ctx context.Context, cursor string) ([]v.Transaction, string, error) {
	if s.Client == nil || s.Table == "" {
		return nil, "", v.ErrInvalid
	}
	in := &dynamodb.ScanInput{TableName: aws.String(s.Table), ConsistentRead: aws.Bool(true), Limit: aws.Int32(5)}
	if cursor != "" {
		in.ExclusiveStartKey = map[string]dt.AttributeValue{"pk": &dt.AttributeValueMemberS{Value: cursor}}
	}
	out, e := s.Client.Scan(ctx, in)
	if e != nil {
		return nil, "", e
	}
	if out == nil {
		return nil, "", v.ErrInvalid
	}
	rows := make([]v.Transaction, 0, len(out.Items))
	for _, item := range out.Items {
		if pk, ok := item["pk"].(*dt.AttributeValueMemberS); ok && strings.HasPrefix(pk.Value, worker.CursorPrefix) {
			continue
		}
		data, ok := item["data"].(*dt.AttributeValueMemberB)
		if !ok {
			return nil, "", errors.New("invalid transaction row")
		}
		var tx v.Transaction
		if e = json.Unmarshal(data.Value, &tx); e != nil {
			return nil, "", e
		}
		if tx.ID == "" || tx.Request.Operation == "" {
			return nil, "", v.ErrInvalid
		}
		rows = append(rows, tx)
	}
	next := ""
	if len(out.LastEvaluatedKey) > 0 {
		key, ok := out.LastEvaluatedKey["pk"].(*dt.AttributeValueMemberS)
		if !ok {
			return nil, "", v.ErrInvalid
		}
		next = key.Value
	}
	return rows, next, nil
}
