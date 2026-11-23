package main

import (
	"context"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
)

type billingSES struct {
	development bool
	mu          sync.Mutex
	client      *ses.Client
}

func (m *billingSES) getClient(ctx context.Context) (*ses.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		return m.client, nil
	}
	var opts []func(*config.LoadOptions) error
	if m.development {
		opts = append(opts, config.WithSharedConfigProfile("aptabase"), config.WithRegion("us-east-1"))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	m.client = ses.NewFromConfig(cfg)
	return m.client, nil
}

func (m *billingSES) send(ctx context.Context, to, subject, body string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := m.getClient(ctx)
	if err != nil {
		return err
	}
	_, err = client.SendEmail(ctx, &ses.SendEmailInput{
		Source:      aws.String("Aptabase <goenning@aptabase.com>"),
		Destination: &types.Destination{ToAddresses: []string{to}},
		Message: &types.Message{
			Subject: &types.Content{Charset: aws.String("UTF-8"), Data: aws.String(subject)},
			Body:    &types.Body{Html: &types.Content{Charset: aws.String("UTF-8"), Data: aws.String(body)}},
		},
	})
	return err
}
