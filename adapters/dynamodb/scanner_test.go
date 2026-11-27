package dynamodb

import (
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dt "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/worker"
	"testing"
)

type scanFake struct {
	in  *ddb.ScanInput
	out *ddb.ScanOutput
}

func (f *scanFake) Scan(_ context.Context, in *ddb.ScanInput, _ ...func(*ddb.Options)) (*ddb.ScanOutput, error) {
	f.in = in
	return f.out, nil
}
func TestScanSkipsCursorAndPreservesPagination(t *testing.T) {
	raw, _ := json.Marshal(v.Transaction{ID: "transaction", Request: v.Request{Operation: "operation"}})
	f := &scanFake{out: &ddb.ScanOutput{Items: []map[string]dt.AttributeValue{
		{"pk": &dt.AttributeValueMemberS{Value: worker.CursorPrefix + "host"}, "data": &dt.AttributeValueMemberB{Value: []byte(`"old-cursor"`)}},
		{"pk": &dt.AttributeValueMemberS{Value: "transaction-key"}, "data": &dt.AttributeValueMemberB{Value: raw}},
	}, LastEvaluatedKey: map[string]dt.AttributeValue{"pk": &dt.AttributeValueMemberS{Value: "last"}}}}
	rows, next, err := (Scanner{Client: f, Table: "verifier"}).Scan(context.Background(), "previous")
	if err != nil || len(rows) != 1 || next != "last" || !aws.ToBool(f.in.ConsistentRead) || f.in.ExclusiveStartKey["pk"].(*dt.AttributeValueMemberS).Value != "previous" {
		t.Fatalf("scan result: %v %s %v", rows, next, err)
	}
}
