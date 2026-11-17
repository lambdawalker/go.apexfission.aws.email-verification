// Command testhost is an IAM-only Lambda executable; it is not an importable API.
package main

import (
	"context"
	"errors"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/internal/testhost"
)

func main() {
	lambda.Start(func(ctx context.Context, in testhost.Input) (testhost.Output, error) {
		app, e := testhost.FromEnvironment(ctx)
		if e != nil {
			return testhost.Output{}, errors.New("test host initialization failed")
		}
		return app.Handle(ctx, in)
	})
}
