package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBillingRenderMail(t *testing.T) {
	for _, name := range []string{"TrialEndsSoon", "UsageLevel80", "UsageLevel90", "UsageLevel100"} {
		body, err := billingRenderMail(name, "Subject <&>", map[string]string{
			"name": "<b>&##URL##", "quota": "200,000,000", "url": "https://example.test/?x=1&y=2",
		})
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		if !strings.Contains(body, "<title>Subject &lt;&amp;&gt;</title>") ||
			!strings.Contains(body, "&lt;b&gt;&amp;##URL##") ||
			!strings.Contains(body, "https://example.test/?x=1&amp;y=2") ||
			strings.Contains(body, "##BODY##") || strings.Contains(body, "##QUOTA##") {
			t.Errorf("incorrect rendered %s: %s", name, body)
		}
	}
}

func TestBillingSMTPHTMLMessage(t *testing.T) {
	from := &mail.Address{Address: "notify@example.test"}
	to := &mail.Address{Address: "user@example.test"}
	const htmlBody = "<p>Hello &amp; goodbye</p>"
	raw, err := billingSMTPMessage(from, to, "Your PHP plan", htmlBody)
	if err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
	if err != nil || string(body) != htmlBody || message.Header.Get("Content-Type") != "text/html; charset=UTF-8" {
		t.Fatalf("invalid SMTP MIME: %v, %q, %v", message.Header, body, err)
	}
	if _, err := billingSMTPMessage(from, to, "invalid\r\nHeader", htmlBody); err == nil {
		t.Error("SMTP accepted a multiline subject")
	}
}

func TestBillingMailSES(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "billing-test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "billing-test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse SES form: %v", err)
		}
		if r.Method != http.MethodPost || !strings.Contains(r.Header.Get("Authorization"), "Credential=billing-test-key/") ||
			r.Form.Get("Action") != "SendEmail" || r.Form.Get("Destination.ToAddresses.member.1") != "owner@example.test" ||
			r.Form.Get("Source") != "Aptabase <goenning@aptabase.com>" || r.Form.Get("Message.Subject.Data") != "Trial subject" ||
			!strings.Contains(r.Form.Get("Message.Body.Html.Data"), "Trial Owner") {
			t.Errorf("unexpected SES request: %s %v", r.Method, r.Form)
		}
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<SendEmailResponse><SendEmailResult><MessageId>test123</MessageId></SendEmailResult></SendEmailResponse>`)
	}))
	defer server.Close()
	t.Setenv("AWS_ENDPOINT_URL_SES", server.URL)
	s := &Server{Config: Config{Region: "US"}}
	if err := s.billingMailer()(t.Context(), "owner@example.test", "Trial subject", "TrialEndsSoon",
		map[string]string{"name": "Trial Owner", "url": "https://example.test"}); err != nil {
		t.Fatal(err)
	}
}

func TestBillingMailCatcherRoundTrip(t *testing.T) {
	smtpURL, webURL := os.Getenv("BILLING_TEST_MAILCATCHER_SMTP_URL"), os.Getenv("BILLING_TEST_MAILCATCHER_WEB_URL")
	if smtpURL == "" || webURL == "" {
		t.Skip("set BILLING_TEST_MAILCATCHER_SMTP_URL and BILLING_TEST_MAILCATCHER_WEB_URL to validate SMTP delivery")
	}
	t.Setenv("ConnectionStrings__mailcatcher", smtpURL)
	t.Setenv("SMTP_HOST", "not-used.example.test")
	s := &Server{Config: Config{Region: "DEV", Development: true}}
	subject := "Billing test " + newID()
	if err := s.billingMailer()(t.Context(), "owner@example.test", subject, "UsageLevel80", map[string]string{
		"name": "Trial Owner", "quota": "100", "url": "https://example.test",
	}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(webURL + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var messages []struct {
		ID      int
		Subject string
	}
	if err := json.NewDecoder(response.Body).Decode(&messages); err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.Subject != subject {
			continue
		}
		html, err := client.Get(fmt.Sprintf("%s/messages/%d.html", webURL, message.ID))
		if err != nil {
			t.Fatal(err)
		}
		defer html.Body.Close()
		content, err := io.ReadAll(html.Body)
		if err != nil || html.StatusCode != http.StatusOK || !bytes.Contains(content, []byte("Hi Trial Owner,")) {
			t.Fatalf("MailCatcher HTML: status %d, body %s (%v)", html.StatusCode, content, err)
		}
		return
	}
	t.Fatal("message not received by MailCatcher")
}
