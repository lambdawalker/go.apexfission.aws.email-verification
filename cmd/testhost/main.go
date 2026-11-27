// Command testhost handles operator invocations and the authenticated mobile demo API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/internal/testhost"
)

func main() {
	// One reproducible ZIP serves two independently permissioned functions.
	// Deployment fixes the mode; event contents cannot select worker privileges.
	if os.Getenv("VERIFICATION_MODE") == "worker" {
		lambda.Start(func(ctx context.Context, raw json.RawMessage) (map[string]string, error) {
			w, err := testhost.WorkerFromEnvironment(ctx)
			if err != nil {
				return nil, errors.New("worker initialization failed")
			}
			return w.Handle(ctx, raw)
		})
		return
	}

	lambda.Start(func(ctx context.Context, raw json.RawMessage) (any, error) {
		var envelope struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, err
		}
		if envelope.Version == "2.0" {
			var event events.APIGatewayV2HTTPRequest
			if err := json.Unmarshal(raw, &event); err != nil {
				return nil, err
			}
			return testhost.Mobile(ctx, event, handle), nil
		}
		var in testhost.Input
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		return handle(ctx, in)
	})
}

func handle(ctx context.Context, in testhost.Input) (testhost.Output, error) {
	app, e := testhost.FromEnvironment(ctx)
	if e != nil {
		return testhost.Output{}, errors.New("test host initialization failed")
	}
	return app.Handle(ctx, in)
}
