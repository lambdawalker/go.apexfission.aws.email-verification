package testhost

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

var mobileActions = map[string]string{
	"POST /v1/verification/start":   "start",
	"POST /v1/verification/confirm": "confirm",
	"POST /v1/verification/status":  "status",
}

func Mobile(ctx context.Context, event events.APIGatewayV2HTTPRequest, handle func(context.Context, Input) (Output, error)) events.APIGatewayV2HTTPResponse {
	auth := event.RequestContext.Authorizer
	if auth == nil || auth.JWT == nil || auth.JWT.Claims["token_use"] != "access" || auth.JWT.Claims["sub"] == "" {
		return mobileResponse(401, Output{Code: "unauthorized"})
	}
	action, ok := mobileActions[event.RouteKey]
	if !ok {
		return mobileResponse(404, Output{Code: "not_found"})
	}
	media, _, _ := mime.ParseMediaType(event.Headers["content-type"])
	if event.IsBase64Encoded || media != "application/json" || len(event.Body) > 8192 {
		return mobileResponse(400, Output{Code: "invalid"})
	}
	var input Input
	decoder := json.NewDecoder(strings.NewReader(event.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF || input.Action != "" || !validID(input.Operation) {
		return mobileResponse(400, Output{Code: "invalid"})
	}
	operation := input.Operation
	// Keep mobile operations scoped to their signed-in user and separate from IAM invocations.
	hash := sha256.Sum256([]byte("mobile-v1\x00" + auth.JWT.Claims["sub"] + "\x00" + operation))
	input.Operation = base64.RawURLEncoding.EncodeToString(hash[:])
	input.Action = action
	out, err := handle(ctx, input)
	if err != nil {
		return mobileResponse(503, Output{Code: "temporarily_unavailable"})
	}
	if out.Operation != "" {
		out.Operation = operation
	}
	switch out.Code {
	case "":
		return mobileResponse(200, out)
	case "invalid":
		return mobileResponse(400, out)
	case "expired":
		return mobileResponse(410, out)
	case "attempts_exhausted":
		return mobileResponse(429, out)
	case "retry_later", "unresolved":
		return mobileResponse(409, out)
	default:
		return mobileResponse(503, Output{Code: "temporarily_unavailable"})
	}
}

func mobileResponse(status int, out Output) events.APIGatewayV2HTTPResponse {
	body, _ := json.Marshal(out)
	return events.APIGatewayV2HTTPResponse{
		StatusCode: status, Body: string(body),
		Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"},
	}
}
