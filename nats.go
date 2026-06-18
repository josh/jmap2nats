package main

import (
	"context"
	"errors"
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
	switch {
	case cfg.NATS.TokenFile != "":
		token, err := readSecretFile(cfg.NATS.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("nats token_file: %w", err)
		}
		opts = append(opts, nats.Token(token))
	case cfg.NATS.User != "" || cfg.NATS.UserFile != "":
		user := cfg.NATS.User
		if cfg.NATS.UserFile != "" {
			u, err := readSecretFile(cfg.NATS.UserFile)
			if err != nil {
				return nil, fmt.Errorf("nats user_file: %w", err)
			}
			user = u
		}
		password, err := readSecretFile(cfg.NATS.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("nats password_file: %w", err)
		}
		opts = append(opts, nats.UserInfo(user, password))
	case cfg.NATS.CredsFile != "":
		opts = append(opts, nats.UserCredentials(cfg.NATS.CredsFile))
	case cfg.NATS.NkeySeedFile != "":
		opt, err := nats.NkeyOptionFromSeed(cfg.NATS.NkeySeedFile)
		if err != nil {
			return nil, fmt.Errorf("load nats nkey: %w", err)
		}
		opts = append(opts, opt)
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

func (n *NATSResources) LastPublishedEmailID(ctx context.Context, accountID string) (string, error) {
	subject := fmt.Sprintf("%s.%s", n.cfg.Stream.SubjectPrefix, accountID)
	msg, err := n.stream.GetLastMsgForSubject(ctx, subject)
	if err != nil {
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("get last msg for %s: %w", subject, err)
	}
	return highWaterEmailID(msg.Header), nil
}

func (n *NATSResources) PutPart(ctx context.Context, accountID, emailID, blobID string, r io.Reader) (string, error) {
	key := partObjectKey(accountID, emailID, blobID)
	_, err := n.parts.Put(ctx, jetstream.ObjectMeta{Name: key}, r)
	if err != nil {
		return "", fmt.Errorf("put object %s: %w", key, err)
	}
	return key, nil
}

func (n *NATSResources) Publish(ctx context.Context, env *envelope, body []byte) (bool, error) {
	subject := fmt.Sprintf("%s.%s", n.cfg.Stream.SubjectPrefix, env.AccountID)
	msg := &nats.Msg{
		Subject: subject,
		Data:    body,
		Header:  env.NATSHeaders(),
	}
	ack, err := n.js.PublishMsg(ctx, msg, jetstream.WithMsgID(messageDedupID(string(env.AccountID), string(env.Email.ID))))
	if err != nil {
		return false, fmt.Errorf("publish %s: %w", subject, err)
	}
	return ack.Duplicate, nil
}

func messageDedupID(accountID, emailID string) string {
	return accountID + "/" + emailID
}

func partObjectKey(accountID, emailID, blobID string) string {
	return accountID + "/" + emailID + "/" + blobID
}

func highWaterEmailID(h nats.Header) string {
	return h.Get("Jmap-Email-Id")
}
