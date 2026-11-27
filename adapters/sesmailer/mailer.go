// Package sesmailer delivers verification messages using Amazon SES v2.
package sesmailer

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	st "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"net/mail"
	"slices"
	"strings"
)

type API interface {
	SendEmail(context.Context, *sesv2.SendEmailInput, ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}
type Message struct{ Subject, Text, HTML string }
type Renderer func(v.Proofs) (Message, error)
type Mailer struct {
	Client API
	Sender string
	// Optional additional restriction, useful for test environments. Host
	// authorization is always required, even when this list is empty.
	AllowedRecipients []string
	Render            Renderer
}

func (m Mailer) Send(ctx context.Context, email string, p v.Proofs) error {
	sender, e := mail.ParseAddress(m.Sender)
	if e != nil || sender.Address != m.Sender || m.Client == nil {
		return v.ErrInvalid
	}
	recipient, e := mail.ParseAddress(email)
	if e != nil || recipient.Address != email {
		return v.ErrInvalid
	}
	if len(m.AllowedRecipients) > 0 && !slices.Contains(m.AllowedRecipients, email) {
		return v.ErrInvalid
	}
	if len(p.Code) != 6 || strings.Trim(p.Code, "0123456789") != "" {
		return v.ErrInvalid
	}
	message := Message{Subject: "Email verification code", Text: "Your verification code is: " + p.Code + "\nReturn to the application to confirm. Ignore this email if you did not request it."}
	if m.Render != nil {
		message, e = m.Render(p)
		if e != nil {
			return e
		}
	}
	if message.Subject == "" || strings.ContainsAny(message.Subject, "\r\n") || (message.Text == "" && message.HTML == "") {
		return v.ErrInvalid
	}
	body := &st.Body{}
	if message.Text != "" {
		body.Text = &st.Content{Data: aws.String(message.Text), Charset: aws.String("UTF-8")}
	}
	if message.HTML != "" {
		body.Html = &st.Content{Data: aws.String(message.HTML), Charset: aws.String("UTF-8")}
	}
	_, e = m.Client.SendEmail(ctx, &sesv2.SendEmailInput{FromEmailAddress: aws.String(m.Sender), Destination: &st.Destination{ToAddresses: []string{email}}, Content: &st.EmailContent{Simple: &st.Message{Subject: &st.Content{Data: aws.String(message.Subject), Charset: aws.String("UTF-8")}, Body: body}}})
	return e
}
