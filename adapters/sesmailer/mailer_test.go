package sesmailer

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"strings"
	"testing"
)

type emailCapture struct{ input *sesv2.SendEmailInput }

func (c *emailCapture) SendEmail(_ context.Context, in *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	c.input = in
	return &sesv2.SendEmailOutput{}, nil
}
func TestSESSendsCodeOnlyToConfiguredRecipient(t *testing.T) {
	api := &emailCapture{}
	m := Mailer{Client: api, Sender: "sender@example.com", AllowedRecipients: []string{"recipient@example.com"}}
	proof := v.Proofs{Link: "secret-link", Code: "123456"}
	if e := m.Send(context.Background(), "other@example.com", proof); e == nil || api.input != nil {
		t.Fatal("recipient restriction failed")
	}
	if e := m.Send(context.Background(), m.AllowedRecipients[0], proof); e != nil {
		t.Fatal(e)
	}
	text := aws.ToString(api.input.Content.Simple.Body.Text.Data)
	if !strings.Contains(text, proof.Code) || strings.Contains(text, proof.Link) {
		t.Fatal("wrong email content")
	}
}

func TestReusableMailerSupportsAuthorizedRecipientsAndBranding(t *testing.T) {
	api := &emailCapture{}
	m := Mailer{Client: api, Sender: "sender@example.com", Render: func(p v.Proofs) (Message, error) {
		return Message{Subject: "My app", Text: "Code: " + p.Code, HTML: "<b>" + p.Code + "</b>"}, nil
	}}
	for _, recipient := range []string{"alice@example.com", "bob@example.com"} {
		if err := m.Send(context.Background(), recipient, v.Proofs{Code: "123456"}); err != nil {
			t.Fatal(err)
		}
		if api.input.Destination.ToAddresses[0] != recipient || aws.ToString(api.input.Content.Simple.Subject.Data) != "My app" {
			t.Fatal("ignored recipient or renderer")
		}
	}
}
