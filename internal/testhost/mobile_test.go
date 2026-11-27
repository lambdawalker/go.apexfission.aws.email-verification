package testhost

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestMobileRouteAndUserIsolation(t *testing.T) {
	operation := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	request := events.APIGatewayV2HTTPRequest{
		RouteKey: "POST /v1/verification/status",
		Headers:  map[string]string{"content-type": "application/json"},
		Body:     `{"operation":"` + operation + `"}`,
	}
	fail := func(context.Context, Input) (Output, error) {
		t.Fatal("unexpected invocation")
		return Output{}, nil
	}
	if response := Mobile(t.Context(), request, fail); response.StatusCode != 401 {
		t.Fatalf("anonymous request status = %d", response.StatusCode)
	}
	request.RequestContext.Authorizer = &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
		JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{
			Claims: map[string]string{"sub": "operator-a", "token_use": "id"},
		},
	}
	if response := Mobile(t.Context(), request, fail); response.StatusCode != 401 {
		t.Fatalf("ID token request status = %d", response.StatusCode)
	}
	claims := request.RequestContext.Authorizer.JWT.Claims
	claims["token_use"] = "access"
	var internalOperations []string
	for _, subject := range []string{"operator-a", "operator-a", "operator-b"} {
		claims["sub"] = subject
		response := Mobile(t.Context(), request, func(_ context.Context, in Input) (Output, error) {
			if in.Action != "status" || in.Operation == operation || !validID(in.Operation) {
				t.Fatalf("unexpected dispatch: %+v", in)
			}
			internalOperations = append(internalOperations, in.Operation)
			return Output{Operation: in.Operation, State: "pending"}, nil
		})
		var out Output
		if response.StatusCode != 200 || response.Headers["Cache-Control"] != "no-store" ||
			json.Unmarshal([]byte(response.Body), &out) != nil || out.Operation != operation {
			t.Fatalf("unexpected response: %+v", response)
		}
	}
	if internalOperations[0] != internalOperations[1] || internalOperations[0] == internalOperations[2] {
		t.Fatal("expected stable, distinct operation IDs per user")
	}
	request.Body = strings.Repeat("x", 8193)
	if got := Mobile(t.Context(), request, fail).StatusCode; got != 400 {
		t.Fatalf("large request status = %d", got)
	}
	request.RouteKey = "POST /v1/verification/tick"
	if got := Mobile(t.Context(), request, fail).StatusCode; got != 404 {
		t.Fatalf("worker route status = %d", got)
	}
}

func TestMobileVerificationAgainstTestHost(t *testing.T) {
	f := newFixture(t)
	op := f.input.Operation
	event := events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"content-type": "application/json"},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{
					Claims: map[string]string{"sub": "demo-user", "token_use": "access"},
				},
			},
		},
	}
	call := func(action string, values map[string]any) Output {
		t.Helper()
		values["operation"] = op
		body, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		event.RouteKey, event.Body = "POST /v1/verification/"+action, string(body)
		got := Mobile(t.Context(), event, func(ctx context.Context, input Input) (Output, error) {
			f.input.Operation = input.Operation
			return f.app.Handle(ctx, input)
		})
		var out Output
		if got.StatusCode != 200 || json.Unmarshal([]byte(got.Body), &out) != nil {
			t.Fatalf("%s: %+v", action, got)
		}
		return out
	}
	start := map[string]any{"email": f.input.Email, "challenge": f.input.Challenge, "authorized_at": f.input.AuthorizedAt}
	if out := call("start", start); out.Operation != op || out.State != "pending" {
		t.Fatal(out)
	}
	if err := f.worker.Step(t.Context(), f.tx(t)); err != nil {
		t.Fatal(err)
	}
	call("confirm", map[string]any{"secret": f.secret, "code": f.mail.proof.Code})
	for i := 0; i < 2; i++ {
		if err := f.worker.Step(t.Context(), f.tx(t)); err != nil {
			t.Fatal(err)
		}
	}
	if out := call("status", map[string]any{}); !out.Accepted || out.Operation != op {
		t.Fatal(out)
	}
}
