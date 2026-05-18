package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type NATSResources struct {
	cfg    Config
	conn   *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream
	parts  jetstream.ObjectStore
}

func ConnectNATS(ctx context.Context, cfg Config, log *slog.Logger) (*NATSResources, error) {
	opts := []nats.Option{
		nats.Name("jmap2nats"),
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
	}
	if cfg.NATS.Creds != "" {
		opts = append(opts, nats.UserCredentials(cfg.NATS.Creds))
	}
	conn, err := nats.Connect(cfg.NATS.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("init jetstream: %w", err)
	}

	var stream jetstream.Stream
	if cfg.Stream.ExternallyManaged {
		stream, err = js.Stream(ctx, cfg.Stream.Name)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("get externally managed stream %s: %w", cfg.Stream.Name, err)
		}
		log.Info("stream verified (externally managed)", "name", cfg.Stream.Name)
	} else {
		stream, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
			Name:       cfg.Stream.Name,
			Subjects:   []string{cfg.Stream.SubjectPrefix + ".>"},
			Retention:  jetstream.LimitsPolicy,
			Discard:    jetstream.DiscardOld,
			Storage:    jetstream.FileStorage,
			MaxAge:     cfg.Stream.MaxAge.Duration(),
			MaxBytes:   cfg.Stream.MaxBytes.Int64(),
			Duplicates: cfg.Stream.DedupWindow.Duration(),
		})
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("ensure stream %s: %w", cfg.Stream.Name, err)
		}
		log.Info("stream ready",
			"name", cfg.Stream.Name,
			"subjects", cfg.Stream.SubjectPrefix+".>",
			"max_age", cfg.Stream.MaxAge.Duration(),
			"max_bytes", cfg.Stream.MaxBytes,
			"dedup_window", cfg.Stream.DedupWindow.Duration(),
		)
	}

	parts, err := js.CreateOrUpdateObjectStore(ctx, jetstream.ObjectStoreConfig{
		Bucket:   cfg.Parts.Bucket,
		TTL:      cfg.Stream.MaxAge.Duration(),
		MaxBytes: cfg.Parts.MaxBytes.Int64(),
		Storage:  jetstream.FileStorage,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ensure object store %s: %w", cfg.Parts.Bucket, err)
	}
	log.Info("object store ready",
		"bucket", cfg.Parts.Bucket,
		"ttl", cfg.Stream.MaxAge.Duration(),
		"max_bytes", cfg.Parts.MaxBytes,
	)

	return &NATSResources{
		cfg:    cfg,
		conn:   conn,
		js:     js,
		stream: stream,
		parts:  parts,
	}, nil
}

func (n *NATSResources) Close() {
	if n.conn != nil {
		n.conn.Close()
	}
}

func (n *NATSResources) PutPart(ctx context.Context, emailID, blobID string, r io.Reader) (string, error) {
	key := emailID + "/" + blobID
	_, err := n.parts.Put(ctx, jetstream.ObjectMeta{Name: key}, r)
	if err != nil {
		return "", fmt.Errorf("put object %s: %w", key, err)
	}
	return key, nil
}

func (n *NATSResources) Publish(ctx context.Context, env *envelope, body []byte) (bool, error) {
	subject := fmt.Sprintf("%s.%s.%s", n.cfg.Stream.SubjectPrefix, env.AccountID, env.Email.ID)
	msg := &nats.Msg{
		Subject: subject,
		Data:    body,
		Header:  env.NATSHeaders(),
	}
	ack, err := n.js.PublishMsg(ctx, msg, jetstream.WithMsgID(string(env.Email.ID)))
	if err != nil {
		return false, fmt.Errorf("publish %s: %w", subject, err)
	}
	return ack.Duplicate, nil
}
