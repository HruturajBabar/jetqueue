package queue

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
)

const StreamName = "JETQUEUE_JOBS"

type Client struct {
	NC *nats.Conn
	JS nats.JetStreamContext
}

func Connect(url string) (*Client, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, err
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}
	return &Client{NC: nc, JS: js}, nil
}

func (c *Client) EnsureStream() error {
	_, err := c.JS.StreamInfo(StreamName)
	if err == nil {
		return nil
	}

	_, err = c.JS.AddStream(&nats.StreamConfig{
		Name:      StreamName,
		Subjects:  []string{"jobs.*"}, // includes jobs.dlq
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
	})
	return err
}

func (c *Client) Publish(ctx context.Context, subject string, data []byte) error {
	_, err := c.JS.Publish(subject, data, nats.Context(ctx))
	return err
}
